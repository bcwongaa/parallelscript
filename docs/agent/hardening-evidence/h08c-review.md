# H08c review reports

The `opus-reviewer` child (claude-opus-5-5, xhigh), read-only, reviewed byte-checked snapshot worktrees of the change. Each round's report is its final message, saved by the parent verbatim under the round's heading; the parent's dispositions are in [h08c.md](h08c.md).

## Round 1 (snapshot `/tmp/h08c/snap1`; probes [h08c-review-probes.tar.gz](h08c-review-probes.tar.gz), sha256 `a904d4af…`)

**Verdict: ready.** I found no correctness bug and no output regression. There is one medium-severity performance cost to record or fix, plus some redundant copies and test-coverage gaps worth noting. Everything below marked "verified" I ran myself against the snapshot `/tmp/h08c/snap1`, the base `/tmp/h08c/base` and Node v24 (`/tmp/h08c/review/tri.sh`). Anything else is marked "inferred".

### Baseline checks (verified)
- **The three tests:** each expect block is exactly what Node prints, with exit 0 and nothing on stderr (`checkexpect.sh`). All three match Node on the snapshot and differ on the base.
- **Unit tests and lint:** `go test ./pts/lower/ ./pts/cgen/ ./pts/ir/` passes; `go vet` and `gofmt -l` are clean.
- **Focused suite** (TestRun, TestRunMatchesNode and TestErrors, filtered to spread, console, dynamic, any_, suspension, yield, await, rest and iterable tests): 184 pass, 0 fail. The 32 skips are files with no expect block. Output is in `suite1v.txt`.
- **Wider suite** (TestCorpusMatchesNode for all 6 themes, plus run tests matching array, tuple, literal, push, math, generic, super, constructor, rest and holes): 482 pass, 0 fail, 90 skips. Output is in `suite2.txt`.
- **Trees untouched:** the snapshot diff fingerprint is unchanged, the base is clean, and the main repo is as I found it.

### Findings, by severity

**F1 (medium, perf, in scope C153, verified): a spread through `any` of a non-array now costs 2.35× the instructions.**
- **Repro:** `p/q_any.pts` calls `f(...held)` 200k times, where `held: any = [1,2,3]` and `f: any`. I measured with `PTS_THREADS=1 valgrind --tool=callgrind`:

  | program | base Ir | snapshot Ir |
  |---|---|---|
  | `q_any` (spread of `held`) | 156,694,786 | 368,560,408 |
  | `q_arr` (typed `number[]`) | 156,694,465 | 156,694,465 |
  | `q_none` (no spread) | 52,152,791 | 52,152,791 |
  | `q_typed` (typed sites, ArraySpread) | 413,555,982 | 413,555,982 |

  That is about 1,060 extra instructions per call.
- **Cause:** `dynamicSpread` (`pts/lower/dynamic.go:193-198`) now sends tuples, typed arrays and arrays held in `any` through `pts_value_to_array`. That function (`pts/runtime/src/dynamic_methods.c:1437-1444`) builds an empty `any_array()` and grows it one `push_any` at a time (`:394-396`, then `with_room`), although `n` is known up front. Typed arrays and strings are copied twice (`pts_value_iterable` converts them first).
- **Suggestions, either one:**
  - Pre-size `pts_value_to_array`, e.g. collect into a buffer and call `pts_array_of(type, values, n)`.
  - Or add a runtime entry that returns arrays, tuples and typed arrays unchanged and drains only the other kinds, then let cgen copy them only when a later argument writes, as `copyChangedSpreads` already does for typed arrays.
- If neither is done now, log the cost in the decision record (H27).

**F2 (low, perf/clarity, in scope, verified in generated C): fresh arrays get copied a second time.**
- `copyChangedSpreads` (`cgen.go:1619`) marks any spread argument followed by a writer, even when its array is already new. From `p/e_double_copy.pts`:
  ```c
  pts_array_copy(pts_value_to_array(pts_value_from_object(pts_frame->s), pts_str_2))
  pts_array_copy(pts_array_slice(pts_frame->rt, 1.0, __builtin_inf()))
  pts_array_copy(pts_frame->kept)   /* H08b's copy, copied again */
  ```
- The results are correct. The fix is to skip marking when the array comes from `ValueToArray`, a slice, copy, concat or spread, a `NewArray`, or a kept-copy local.
- Older but nearby: `appended` (`arrays.go:165`) still copies the empty first `NewArray`, which is always new, so every spread makes one extra allocation. That predates H08c.

**F3 (low, pre-existing, out of scope; suggest H29 or with C143 in H22, verified): a getter-valued method through `any` runs after the arguments.**
- `p/k_method_getter.pts`: `o.m(xs.length, grow())`, where `o: any` has `get m()` that pushes to `xs`.
  - Node prints `3/10`; base and snapshot print `2/10`.
  - With a spread and a later writer, Node prints `1/2/9/10`, the base `1/2/100/10`, the snapshot `1/2/10`. The output changed from one wrong value to another, so this is not a regression.
- **Cause:** without a suspension, `DynamicCallMethod` looks the method up inside the runtime, after the arguments. D-187 only fixed the form with a suspension (`DynamicCallFound`). C143 covers the throwing case; I found no entry for the getter-ordering case.

**F4 (info, as the parent expected): "not iterable" messages differ from V8. Only the error name is pinned. Logged for H22.**

| case | PTS | V8 |
|---|---|---|
| number | `(nums as any) is not iterable` | `number 5 is not iterable (cannot read property Symbol(Symbol.iterator))` |
| undefined | `(undefined as any) is not iterable` | `undefined is not iterable (cannot read property Symbol(Symbol.iterator))` |
| null | `(null as any) is not iterable` | `object null is not iterable (cannot read property Symbol(Symbol.iterator))` |
| plain object | `({ a: 1 } as any) is not iterable` | `object is not iterable (cannot read property Symbol(Symbol.iterator))` |
| array-like | `arrayLike is not iterable` | `Spread syntax requires ...iterable[Symbol.iterator] to be a function` |

**F5 (info, pre-existing S4, unchanged, verified):** `fn.apply(null, "ab")` and `fn.apply(null, new Set([1]))` through `any` still throw the `Cannot spread … through any` TypeError. For the Set, Node calls with no arguments. `function_apply` and `pts_spread_length` were not touched.

### Checks with no problem found (verified unless marked)

**1. `spread` in `arrays.go`.** Probes `a_spread_kinds`, `b_suspend`, `w_order_misc` and `g_sites` all match Node; the only difference is D-085's intended `number[]` holes.
- **Operand kinds:** conditional and logical operands, nested spreads three deep, an operand that throws (the array is left as JS leaves it), checked copies of other element types, tuples into arrays, typed arrays, `...xs.splice()`, self-pushes and the push receiver order all match.
- **Holes:** `(number|undefined)[]`, `any[]` and `(string|undefined)[]` match.
- **Suspensions:** generators, async functions and async generators with `yield`/`await` among the operands match.
- **Other lowering passes:** none assumed the operands were evaluated first. `ArraySpread` is built in only two places (`lower/arrays.go:166`, `lower/lower.go:2039`). Its effects (`readsLocals` plus the operands') still describe it correctly.
- **Suspensions and GC (inferred from code):**
  - No statement-expression local outlives a suspension, because lowering hoists suspensions out of expressions (D-109).
  - The result temporary is a C local, which bdwgc scans conservatively, the same as the temporaries before.
  - All the runtime helpers involved are functions, not macros, so inline arguments are safe.
- **Readability:** the C is shorter than before and reads well.

**2. `copyChangedSpreads`.** Probes `c_console_effects`, `j_conversions`, `f_holes_console`, `n_found_after_await` and `l_console_stderr` all match.
- **Later operands that trigger a copy:** class getters, interface fields backed by getters, object-literal getters, `toString`/`valueOf` (templates, `+`, `String()`), any getters and arithmetic through `any`, later spreads of generators and iterables, `pop`, `length = 0`, element writes, sort and map callbacks, `toJSON`.
- **Why "a later operand may write" is enough:** an operand that can only throw stops the call, so nothing reads the spread. A later `ValueToArray` counts as a write.
- **Every spread argument's array is an array:** console gets arrays, rest-tuple slices, tuple copies or `ValueToArray`; dynamic calls get an array or `ValueToArray`. So `pts_array_copy` is always valid.
- **Unset fields:** they still throw a TypeError at the read (`p/m_unset_field.pts`).
- **Frozen arrays and holes:** both match through console and `any`.
- **All three dynamic builtins and H08b:** checked, including H08b's kept copies.

**3. `dynamicSpread`.** Probes `d_dynamic_spread`, `o_more_kinds`, `i_await_iterables`, `h_rest_param` and `u_typed_kinds` all match apart from F4/F5.
- **Kinds that now match Node:** optional tuples (whether left out, given or undefined), rest tuples, readonly arrays, unions, `Iterable`/`IterableIterator`/`Generator` (an exhausted one too), custom iterator objects, astral and accented strings, map/set/array iterators, typed arrays, `any[]` holes, empty sets, two non-array spreads, rest parameters (base got `viaTuple` wrong), function-union callees, `?.` calls, `call`/`bind` with spread sets, and `fn.apply(...[null, [4, 5]])`.
- **Generators** are drained once, at the spread, before later arguments run.
- **Non-iterables** throw before the later arguments run.
- **No regressions:** nothing that worked on the base works differently now.

**4. Other spread sites, checked against Node.**
- **Read correctly (base got the starred ones wrong):** `a.concat` (JS reads its arguments at the call, and that behaviour is kept), `Float64Array.from([...])`\*, `JSON.stringify([...])`\*, `Promise.all([...])`\*, `Array.from(…, fn)`\*, generic constructors\*, `?.()`\*, nested rows, destructuring defaults, `str.concat`, `Math.max` with a later spread, and `pts:path` `join`/`posix.join` (base printed `q/b/zz/c`, the snapshot and Node print `a/b/c`).
- **Still PTS1001, unchanged and pre-existing:** `Float64Array.of(...)`, `bind(...xs)` on typed functions, `apply` with a non-literal array, `Object.assign({}, ...)`, typed `Object.assign` sources, `concat(...xs)`, `reduce(cb, ...t)`, tagged templates.

### Tests
- **Honest:** expect blocks are Node's output, names describe behaviour, the original lines of the unskipped test are unchanged, and only additions were made.
- **Gaps worth pinning (low):**
  - (a) When a generator spread through `any` runs relative to a later argument. Nothing pins "drained at the spread".
  - (b) A function-union callee given a Set (`closures.go` goes through `dynamicCallWith`), which changed from a throw to working.
  - (c) The console and `any` copy paths with a `(number|undefined)[]` hole filled by a later argument.
  - (d) `DynamicCallFound` with a spread after the `await` followed by a writer (`n_found_after_await`).
  - (e) `console.error`/`warn`/`assert` with spreads, using `// stderr:` lines. The same runtime path is already pinned through `console.group`.
- **Doc nit:** the `ArraySpread` comment in `ir.go:883` could say each part is read at its place, before the next operand, which cgen now guarantees.

The probe archive is at `/tmp/h08c/h08c-review-probes.tar.gz`; `cmds.txt` inside lists every command and its result. `/tmp/h08c/review.done` holds its sha256, `a904d4af34e7d1f45defc298e101ab0db4c0824d9a33c63d9fbecf091db328b2`.

Files I looked at:
- `/tmp/h08c/snap1/pts/cgen/arrays.go`
- `/tmp/h08c/snap1/pts/cgen/cgen.go`
- `/tmp/h08c/snap1/pts/lower/dynamic.go`
- `/tmp/h08c/snap1/pts/runtime/src/dynamic_methods.c`
- `/tmp/h08c/snap1/pts/tests/run/any_call_spreads_iterables.pts`
- `/tmp/h08c/snap1/pts/tests/run/spreads_read_before_later_operands_at_every_site.pts`
- `/tmp/h08c/snap1/pts/tests/run/spread_elements_read_before_later_arguments.pts`

## Round 2 (snapshot `/tmp/h08c/snap2`; probes [h08c-review2-probes.tar.gz](h08c-review2-probes.tar.gz), sha256 `60baeb97…`)

Round 1's reviewer, resumed with its probes.

**Round 2 verdict: ready.** The revised change is correct and the F1 cost is fixed. I found no correctness bug, race or GC problem, and nothing in the delta changes output. Three small optional suggestions are listed at the end. Everything below I ran myself unless marked "inferred".

### What I checked
- **Delta:** I diffed snapshot 1 against snapshot 2 (`/tmp/h08c/review2/delta.diff`). `arrays.go` and the three round-1 tests are byte-identical. The changes are in `cgen.go` (`copyChangedSpreads` with `isNewArray`, and `spreadCopy`), `ir.go` (`ValueSpreadItems` and docs), `lower/dynamic.go`, `pts.h`, `dynamic_methods.c` and `dynamic_test.c`, plus the new test.
- **The four tests:** each expect block matches Node's stdout, and each `// stderr:` line appears in Node's stderr. All four match Node on snapshot 2 and differ on the base.
- **The new test without its C153 lines** (`p/z_variant_no_c153.pts`): the base gets every C152 line wrong (`1/7/3/10`, `0|5|50|2|100|10`, …), then throws C153 at the Set. So the test pins both bugs.
- **Round-1 probes:** I reran all 17 with the new CLI. Their outputs are byte-identical to snapshot 1's. The differences from Node that remain are the ones already recorded: D-085 holes, F3, F4, F5, and one probe of mine that was unsound.
- **Runtime C test:** `dynamic_test.c` passes under the sanitizers.
- **Go:** `go test ./pts/lower/ ./pts/cgen/` passes; `gofmt` and `go vet` are clean.
- **Focused suite:** 186 pass, 0 fail, 32 skips (round 1's 184 plus the new test under TestRun and TestRunMatchesNode).

### Round-1 dispositions
**F1 (perf): fixed.** Callgrind instruction counts:

| program | base | snapshot 2 |
|---|---|---|
| `q_any` (spread of an `any`-held array) | 156,694,144 | 161,894,582 (+26 per call; round 1 was 368.6M) |
| `q_tuple` (typed tuple through `any`) | 136,487,643 | 141,687,840 |
| `q_arr`, `q_typed`, `q_none` | unchanged | unchanged |
| `q_writer` (`f(...held, k++)`) | 187,692,022 | 328,203,391 |

The `q_writer` increase is the copy at the spread that correctness needs; the base gives the wrong output there.

**F2 (redundant copies): fixed.** The generated C no longer copies a new array a second time. A rest-tuple slice is now `pts_value_from_object(pts_array_slice(...))`, and a non-array spread followed by a writer gets a single `pts_value_to_array`. As documented, H08b's `pts_frame->kept` is still copied once more.

**F3, F4, F5:** recorded in the ledger as you described, no code change.

### Contract of `pts_value_spread_items`
**Every live value it returns is copied before a later writer runs.**
- `ValueSpreadItems` is built only in `dynamicSpread`, which only `dynamicCallWith` calls. That covers `DynamicCall`, `DynamicCallMethod`, `DynamicCallFound` and function-union callees. Console never sees it: it still uses `ValueToArray` through `argumentValues`.
- A spread argument is only emitted by `callBuiltin`, which always runs `copyChangedSpreads` first.
- `isNewArray` leaves out `ValueSpreadItems`, so it is marked whenever a later argument may write, and `spreadCopy` then makes a copy at the spread.
- `p/x_live_items.pts` matches Node in all 17 cases. These cover an `any` array followed by:
  - a writer, a later generator spread, a getter, or a nested dynamic call that pushes;
  - a writer inside a later spread, or the same array spread again around a writer.

  They also cover `.call` and `.bind` through `any`, function-union callees with a tuple or a `Float32Array`, and the found-callee form before an await, after it, and with a writer after it. Without a writer the value is read at the call and the output is still correct.
- `p/y_match_frozen.pts` matches Node: regexp match arrays with undefined groups, frozen arrays, an `any` string or tuple with a writer, and an optional tuple element filled by a later argument.

**H08b's kept copies.** A tuple, typed array or `any` value kept before a suspension is a private snapshot. Reaching `ValueSpreadItems` unchanged is therefore safe; a later writer only adds a redundant copy.

**Races (D-130), inferred from code.**
- `pts_value_to_array` reads `n` once and writes exactly `n` slots, so an array that grows meanwhile can't overflow the buffer.
- **Arrays:** `pts_array_element` goes through `pts_array_slot_or_null`. That checks against the smaller of the current length and the buffer capacity, after an acquire load of the buffer. A read past a shrunk length gives `undefined`, never an out-of-bounds read.
- **Tuples:** `pts_tuple_length` is at most the field count and the fields never change, so the read is safe.
- **Typed arrays:** `pts_value_iterable` converts them to an array first. The unchanged form that the runtime spreads at the call keeps the existing two-pass guard in `spread_arguments` (`n < total`).
- None of this is new exposure: the old `push_any` loop read the same way.

**GC (inferred).** The buffer comes from `new_buffer`, which uses `GC_MALLOC` (scanned and zeroed) for kinds that hold pointers, so `PTS_FIELD_VALUE` is scanned. The array stays reachable from a C local while `pts_spread_element` may allocate. The plain stores into a fresh array are the same pattern `pts_array_keys` uses, and holes stay as before.

**Effects.** `ValueSpreadItems` falls to the default `writes|mayThrow` in `order.go`. That is correct, since it can run user iterators and throw. Its `Name: "spread"` is only used by `print.go`, so it can't collide with anything.

**C readability.** The output is fine: `pts_value_spread_items(pts_value_from_object(s), pts_str_2)`, or with a later writer `pts_value_from_object(pts_value_to_array(..., pts_str_N))`.

### Optional suggestions (low; none blocks)
1. **Perf (inferred from code):** `pts_value_spread_items` could return `pts_value_iterable`'s result directly for strings, sets, maps, generators and object iterables. Those are already new arrays, and `spread_arguments` can spread any array kind. Today each of those spreads allocates twice (the iterable's array, then the pre-sized copy); `q_set` costs about 2,450 instructions per call.
2. **Hardening:** console's `spread_arguments` (`console.c:86-104`) treats every spread argument as a `pts_array`. Nothing produces anything else today, but a cgen panic when a console builtin gets a spread argument whose value isn't array-typed would turn any future misuse into an internal error rather than memory-unsafe C.
3. **Unit test coverage:** `a_spread_through_any_drains_what_the_call_cannot_spread` checks an array returned unchanged, a string drained and a number throwing. It could also check that a tuple and a typed array come back unchanged and that a Set is drained.

The probe archive is at `/tmp/h08c/h08c-review2-probes.tar.gz`; `cmds.txt` inside lists every command and its result. `/tmp/h08c/review2.done` holds its sha256, `60baeb9797ce087d625071bbde03f6124bf515406c7467ab61a90b051d3e552c`. I edited nothing in `/tmp/h08c/snap2`, `/tmp/h08c/base` or the repository.

Files I looked at:
- `/tmp/h08c/snap2/pts/cgen/cgen.go`
- `/tmp/h08c/snap2/pts/lower/dynamic.go`
- `/tmp/h08c/snap2/pts/ir/ir.go`
- `/tmp/h08c/snap2/pts/runtime/src/dynamic_methods.c`
- `/tmp/h08c/snap2/pts/runtime/include/pts.h`
- `/tmp/h08c/snap2/pts/runtime/tests/dynamic_test.c`
- `/tmp/h08c/snap2/pts/tests/run/any_and_console_spreads_read_at_their_place.pts`

## Round 3 (snapshot `/tmp/h08c/snap3`, the final code; probes [h08c-review3-probes.tar.gz](h08c-review3-probes.tar.gz), sha256 `cbf627ed…`)

Round 2's reviewer, resumed, time-boxed to the delta.

**Round 3 verdict: ready.** I reviewed the code delta from snapshot 2 to snapshot 3 (`/tmp/h08c/review3/delta_pts.diff`). The new console guard, the spread-items change and the extended C test all check out, no program output changed, and the q_* costs are the ones you reported. I edited nothing in the snapshots or the repository, and ran only one go test at a time.

### The three changes
**1. `pts_value_spread_items` now returns `pts_value_iterable`'s new array** (`dynamic_methods.c:1448-1456`).
- **What it can return** (checked against the code): arrays, tuples and typed arrays still come back unchanged, so the typed-array conversion inside `pts_value_iterable` is never reached from here. Everything else comes back as a new array:
  - strings: `pts_array_from_string`, a `string[]`;
  - sets and maps: an `any[]`;
  - generators and object iterables: `pts_iterator_to_array`, an `any[]`.
- **`spread_arguments` handles every one of these kinds.** `pts_spread_length` and `pts_spread_element` only look at whether the object is an array, then read through `pts_array_element` and `pts_value_of_slot`, which accept `PTS_FIELD_STRING` as well as `PTS_FIELD_VALUE`.
- **I ran it** with `p/z_items_kinds.pts`, which matches Node on every line. It covers:
  - a string with an accent and an astral character, an `any`-held string, and a string followed by a writer (the copy path);
  - a mixed Set holding an object, null and undefined;
  - Map entries and a generator yielding undefined, plus a writer after the generator;
  - a 1,001-code-point string;
  - method and `.call` forms through `any`.
- **No consumer assumes the result is an `any[]`.** The only ones are the runtime's argument spreading and `spreadCopy`, which only looks at the call's own arguments. The IR result type is `Value`. Freshness doesn't matter for correctness, because `isNewArray` doesn't count `ValueSpreadItems` as new.

**2. The panic in `spreadArgs`** (`cgen.go:1612-1616`) only fires for console methods whose spread argument isn't array-typed. `ValueArray` is an `*ir.Array`, so legitimate spreads pass. I checked this with console probes `c_console_effects`, `f_holes_console`, `l_console_stderr` and `h_rest_param` (all match Node), `emit-c` of `e_double_copy.pts` (rest-tuple slices), and `go test ./pts/cgen/` (passes). Before this change, such an argument would already have failed in `boxAs`, so no new failure mode appears.

**3. The extended `dynamic_test.c`** passes under the sanitizers.

### Reruns with the snap3 CLI
- `x_live_items`, `y_match_frozen`, `o_more_kinds`, `i_await_iterables` and the four H08c tests all match Node.
- Those probes and `d_dynamic_spread` give output byte-identical to snapshot 2. `d_dynamic_spread` still differs from Node only in the recorded D-085, F4 and F5 lines.

Callgrind instruction counts, snapshot 3:

| program | base | snapshot 2 | snapshot 3 |
|---|---|---|---|
| `q_any` | 156,694,144 | 161,894,582 | 159,694,365 (about +15 per call over base) |
| `q_tuple` | 136,487,643 | 141,687,840 | 139,487,606 |
| `q_set` | (throws C153) | 490,826,080 | 358,183,936 |
| `q_writer` | 187,692,022 | 328,203,391 | 328,210,131 |
| `q_arr` | 156,694,391 | 156,694,385 | 156,695,035 |
| `q_typed` | 413,565,148 | 413,456,444 | 413,456,009 |
| `q_none` | 52,151,447 | 52,157,355 | 52,152,848 |

`q_writer` is unchanged: `k++` counts as a write, so the copy is still made, as you plan to document.

### Minor, not blocking
`pts_value_spread_items` hands strings over as `string[]`, while `spreadCopy`'s `pts_value_to_array` gives an `any[]`. That asymmetry is fine because the runtime spreads both identically, and the updated comments in `pts.h`, `ir.go` and `dynamic.go` already say "a new array of any element kind".

The probe archive is at `/tmp/h08c/h08c-review3-probes.tar.gz`; `cmds.txt` inside lists the commands and results. `/tmp/h08c/review3.done` holds its sha256, `cbf627ede9eda6bb3e13160dbd38e1050bb535e35e4da7413e124284f8b2dd78`.
