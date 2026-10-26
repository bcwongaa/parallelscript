# H35b review

The independent `reviewer` (claude-opus-5-5, xhigh, read-only), reviewing snapshot `/tmp/h35b/snap3` (the implementation, `5fd81c5c9`). Each round's report as it returned it; the parent's resolution of each finding is in [h35b.md](h35b.md).

## Round 1 (2026-10-26): NOT READY

Probes: [h35b-review1-probes.tar.gz](h35b-review1-probes.tar.gz).

**Verdict: NOT READY.** One high-severity gap in completeness (R1-F1). The publication checker's default rules hide that gap, and other classes of missing barrier, so the tests that claim to cover it can't fail (R1-F2). Three smaller construction gaps and some doc and test inaccuracies come after that.

**Setup**
- I reviewed the snapshot at `/tmp/h35b/snap3`: its CLI with `PTS_HOME=/tmp/h35b/snap3`, against the base at `/tmp/h35b/wt-base` and Node v24.21.0.
- I edited nothing in the repository. Mutants were built only through `go test/build -overlay` and copied PTS_HOMEs under `/tmp/h35b/review1/homes`.
- Archive: `/tmp/h35b/review1/h35b-review1-probes.tar.gz`, with an `INDEX.txt` listing scripts and probes. It holds no binaries and no keys. `scripts/mk_mutants.sh` rebuilds every mutant.
- No arm hardware was used. Arm-side claims rest on the generated C, clang 18's aarch64 assembly and the ARMv8 model.
- One process-hygiene slip: early on I ran `pkill -f` against my own sweep script. It killed only that script and my own shell, but it broke the "never `pkill -f`" rule. After that I killed only by PID.

## Checked and found sound

- **ARMv8 reasoning.**
  - Barrier plus address dependency (MP+dmb.st+addr) is forbidden by the ARMv8 model, so a reader can't see stale fields.
  - The relay case (a third thread gets the pointer from a thread that loaded it, ISA2/WRC with a data dependency) is also forbidden. ARMv8 is other-multi-copy-atomic, so the ordering edges form a cycle.
  - The ready flags (release store, acquire load on aarch64) are sound.
- **Typed reads of unset fields and holes never crash.** I checked string fields, object fields, holes in `T[]` and `string[]`, and for-of, map, destructuring, join, sort and JSON over them (`holes_typed.pts`, `unset_string_*.pts`). They return empty or zero values or throw TypeError or RangeError. So a stale value never crashes, which matches D-141.
- **The static initialisers for literal globals are safe.**
  - In an import cycle, reads before the declaration throw ReferenceError on base, snapshot and Node alike (`probes/cycle/`).
  - Union-typed, NaN, Infinity, -0 and enum initialisers either get no static value or the right one; output is identical to the base (`literal_globals.pts`).
  - `var` is rejected by both CLIs.
- **x86 compile order.** `scripts/flag_order.py` compiled 1,429 run, corpus, stress and benchmark programs at -O2 for x86-64 (29 don't build by design). It looked, block by block, for a binding's load placed above its flag's load.
  - The two hits were both false positives: the binding was loaded after an earlier test of the same flag.
  - So clang 18 never hoisted a binding above its flag in this corpus. That is evidence, not a guarantee, like the address-dependency argument.
- **x86 cost.** My callgrind run, one thread, base against snapshot:

  | Benchmark | Base instructions | Snapshot instructions | Change |
  |---|---|---|---|
  | string_building | 6,965,370,224 | 6,966,006,137 | +0.009% |
  | json | 7,628,100,921 | 7,627,874,262 | −0.003% |

  `pts_publish` emits no instruction on x86, `PTS_SET_READY` is a plain `mov`, and the parent's round-2 numbers are consistent with mine.
- **Mutations that the suite kills.**
  - `PTS_READY` made plain under TSan: `module_binding.pts` and `early_cell.pts` fail with "ThreadSanitizer: data race".
  - No barrier in `newObject`: `publication/object_literal` fails on all three thread counts.
  - No barrier after `new C()`: 14 programs fail (`run/classes`, `stress/race_*`, `publication/subclass`, …).
- **Default-mode sweeps are clean.** Over 160 API results (strings, collections, cross-loop tasks, channels, generators, timers, promises), `--check all` reports nothing and the `stats` line shows the checker saw them.
- **The AbortSignal lock can't deadlock by re-entry.** I found no read of `reason` or call of `throwIfAborted` while that signal's own lock is held.

---

### R1-F1 (high): generated builders publish arrays, maps and records with nothing ordering their fill

**What happens.** These constructions make their result through a maker that publishes it empty. They then fill it with element stores, or with pushes or sets whose release stores readers acquire. Then they return it with no `pts_publish` after the fill:
- typed `map` (`lower/callbacks.go`: `pts_array_of_length`, then `PTS_ARRAY_SET`), `filter`, `flatMap`, `Array.from(x, f)`;
- a typed array's `map` (`typedMap`);
- `Object.keys`, `values` and `entries`, and `Array.from(map.keys())`, all as generated helpers;
- `new Map(entries)` (the `Map_of` helper);
- record literals (`pts_record_set` chains);
- spreads and `concat` (`appended` in `cgen/arrays.go`);
- typed `toSorted`, `toReversed` and `toSpliced` (a copy, then an in-place sort, reverse or splice);
- dynamic-call rest arrays (`dynamicArguments`).

A plain store then hands the result to other threads. On aarch64 nothing orders the fill before that store. The runtime's dynamic versions of the same methods (`array_map`, `array_to_sorted`, …) were given barriers; the typed lowerings, which are far more common, were not.

**What another thread can see on arm** (no crash, since holes are checked):
- `doubled = nums.map(n => n * 2)` read as `[0,0,0,…]`;
- `names[0]` of a `map` result throwing "Array index 0 is out of range";
- `new Map([["a", 1]])` or `{alpha: 1}` (as a Record) read as empty;
- a spread read as a prefix of itself;
- a `toSorted` result read with duplicated elements.

D-226, the new SPEC §4.1 bullet, the GUIDE and ARCHITECTURE rule 6 all promise "sees it as it was when its construction ended … never a half-made object".

**Repro:**
```
cd /tmp/h35b/review1/probes
python3 ../scripts/run.py --profile publication --check all typed_map_results.pts          # exit=0
python3 ../scripts/run.py --profile publication --check all,strict typed_map_results.pts   # exit=70: doubled, names, scaled (allocated in pts_array_of_length / new_typed_array)
python3 ../scripts/run.py --profile publication --check all,partial typed_map_results.pts  # exit=70: kept (filter), squares (Array.from)
python3 ../scripts/run.py --profile publication --check all,strict,partial api_sweep_typed.pts  # reports lines 13-17,19,21-23,30,39,45: map, filter, flatMap, spread, toSorted/Reversed/Spliced, new Map(entries), Float64Array map, map(String)
PTS_HOME=/tmp/h35b/snap3 /tmp/h35b/snap3/pts-bin/pts emit-c typed_map_results.pts | sed -n '/^static pts_array \*pts_fn_Array_map(/,/^}/p'   # no pts_publish before `return result;`
```

The aarch64 code clang 18 makes for `doubled = nums.map(...)` (in `out/typed_map_results.a64.s`, lines 117–172) has no `dmb` between the element stores and the publishing store:
```
bl  pts_array_of_length        // its barrier is here, before the fill
... loop: blr x8 (callback); bl pts_array_set
.LBB0_8:
str x20, [x28, :lo12:pts_global_doubled]   // no dmb between the fill and this store
```

The shipped tests contain these cases without noticing them:
- `publication/arrays.pts`: `items = numbers.map(...)`;
- `publication/tuples_spreads.pts`: `joined = [...xs, ...xs.map(...), 99]`;
- `publication/typed_arrays.pts`: `view = ….subarray(1, 4).map(...)`.

Strict+partial mode reports all three (`out/sp_*.txt`).

**Suggested fix.** Emit `pts_publish()` before the `return` of every lowered helper that builds a result. That covers the helpers in `lower/callbacks.go` and `lower/maps.go`, the `Map_of` / `Object_*` / `Array_from_*` helpers, the copy-then-sort/reverse/splice lowerings, the `pts_record_set` chain's final value, `appended`'s final value and the rest arrays built in `dynamicArguments`. Doing it as an `ir.Publish` statement, or at cgen's `Return` of a helper, keeps it in one place. Add strict-mode tests for these programs (see R1-F2). On x86 it costs nothing.

### R1-F2 (medium): the checker's default rules hide whole classes of missing barrier; several tests prove less than they say

Three rules make a missing barrier invisible in default mode:
- `PTS_PUBLICATION_SLOTS`: first stores into array and ArrayBuffer elements are never fills.
- A release store (`PTS_PUBLICATION_RELEASE`) cleans the object that holds it (for example the array header), although it doesn't order the stores made before it with a later plain store.
- The first-store rule: pts pre-stores `""` into every string field, so the real value is never a fill. memcpy fills are invisible anyway.

**Mutation evidence:**
- `homes/runtime-drops` removes 11 runtime barriers. `runtime_drops.pts` (`run_home.sh HOME all`) reports `new_date`, `regexp_new`, `items_iterator_new`, `bind_method` and `pts_array_of`. It does not report the dynamic `map`, `split`, `new Map(map)` or `new Float64Array(array)`: each of those mutants survives.
- `release_hides_header.pts` under the same home: the `pts_array_of` barrier is removed, so the array's type word and buffer pointer are unordered. That is a crash-level gap on arm. The literal `[1, 2]` is reported, but `const a = []; a.push(1); shared = a` is not, because the push's release cleaned the header.
- With `thisEscapes` disabled (`go test -overlay mut/overlay_noescape.json -run TestNewObjectsReachOtherThreadsFilled ./pts/tests/`), the whole test passes (`ok 38.260s`).
  - `escaping_this.pts` escapes through `registry.push(this)`, whose release orders it anyway.
  - `this.name` was pre-set to `""`, so its later store isn't a fill.
  - Only the cgen text test catches the mutant.
  - With a number field, the plain-store escape is caught: `escaping_this_plain2.pts` exits 0 on the snapshot and 70 with `--exe` set to the mutant CLI (rebuilt by `scripts/mk_mutants.sh`, then `go build -overlay … -o /tmp/h35b/review1/bin/pts-noescape ./cmd/pts`).
- With no barrier after `new C()` (`overlay_noinst.json`), `publication/new_instance.pts` passes, because `label` was pre-stored and `x` and `y` precede the template string's barrier. Other tests do catch this mutant.

**Effect.** R1-F1 went unseen, and the comments of `arrays.pts` ("by map and by Array.from … must reach other threads first"), `escaping_this.pts` and `new_instance.pts` claim coverage the checker can't give. The litmus `stress/race_publication.pts`, the only test for arm hardware, publishes only static string literals (`this->label = …pts_global_LABELS…`, whose elements are `pts_str_1..4`). It never publishes runtime-made strings, arrays, maps or records, although ARCHITECTURE says "new objects of every kind".

**Suggested fixes:**
- On a release store, don't clean the holding object, only the object it points to and `also`.
- Add a strict-mode run, `PTS_PUBLICATION_CHECK=all,strict`, over construction-only programs: the API sweeps here, rewritten so nothing is mutated after construction.
- Make `escaping_this.pts` and `new_instance.pts` escape through plain stores after number-field fills.
- Add runtime-made strings, `map` results, records and `new Map` to the litmus.

### R1-F3 (low): `thisEscapes` puts the barrier at the read of `this`, not where the instance escapes

Three cases are missed:
- **Later operands.** `register(this, this.id = nextId++)` compiles to `pts_tmp1 = (pts_publish(), this); pts_tmp2 = (this->id = …)`, so the store comes after the barrier. Repro: `arg_order_escape2.pts`, `--check all`, exits 70.
- **Aliases.** `const self = this; this.score = n * 2; holder.node = self;` puts the barrier at the alias. Repro: `this_alias_escape.pts` exits 70.
- **Methods and functions called with `this`.** `this.init()`, where `init` fills fields and then stores `this`, exits 70 (`ctor_method_escape.pts`). The SPEC's "publishes what it stored so far there" covers this one, but a nightly `PTS_BUILD_PROFILE=publication` run would fail on such a program.

On arm, readers see zero or default fields that x86 always shows set. D-226's Known gaps list only the variant where a closure was made earlier.

**Suggested fix.** Put the barrier in the call's or store's prelude, after all operands. Treat locals initialised from `this` as `this`. List the method case in D-226's Known gaps, or have the checker accept it.

### R1-F4 (medium): runtime makers that fill after their maker's barrier

Not pre-existing in the sense that matters: the base has no barriers at all.
- `fs.c:298`, `readFileSync(path)` without an encoding: `pts_typed_array_new` publishes, then `memcpy` fills the bytes, then the array is returned. Another thread can see the file as zeros. The checker can't see memcpy (`read_file_bytes.pts` exits 0 in every mode).
- `fs.c` SCANDIR, `readdirSync`: the last name is pushed after the last barrier.
- `dynamic_methods.c` `pts_value_values` (`Object.values` through `any`) has no `pts_publish`, though its siblings `keys` and `entries` do.
- `pts_fs_rm_directory_error` and `fetch.c` `fail_system` overwrite `code` / `errno_value` after `pts_system_error_new`'s barrier. Another thread may see "EISDIR" instead of "ERR_FS_EISDIR".
- `JSON.parse` with a reviver: the revived replacements are stored after `pts_json_parse`'s barrier.

**Fix:** a `pts_publish()` before each of these returns.

### R1-F5 (low / nit): docs

- ARCHITECTURE §4.13 rule 6 says flags are "tested with an acquire load". That is true only on aarch64 and under TSan; x86-64 uses a plain load, and literal bindings use `PTS_READY_FLAG`.
- The "How it is tested" paragraph omits the publication checker and its nightly job.
- The GUIDE states the arm guarantee as fact, with no "not run on arm hardware" caveat. R1-F1 contradicts it. The README is more careful.
- D-226 says x86-64 pays "no cost". `h35b.md` records mandelbrot at +5.4% instructions; the time measured is not worse.
- D-226 cites "the allocation-side publication HotSpot and Go use". Go's `publicationBarrier` publishes the allocator's state, not the program's fields.
- `PTS_PUBLISH` (no such macro) appears in `publication_check.c:5` and `pts/tests/README.md:16`. It should be `pts_publish`.
- `pts help` doesn't list `PTS_BUILD_PROFILE`, and a bad value exits 1, the code for a compile error.

**VERDICT: NOT READY**

## Round 2 (2026-10-26): NOT READY

On snapshot `/tmp/h35b/snap4` (round 1's fixes, `38a1e681c`). Probes: [h35b-review2-probes.tar.gz](h35b-review2-probes.tar.gz).

**Verdict: NOT READY.** The parallel library's builders (`.par.map` and friends) have the same gap round 1 found in the typed builders (R2-F1, high). The fix for `this` aliases also introduced a small behaviour regression against Node (R2-F2, low), and the new strict test has a few uncovered barriers (R2-F3, low). Everything round 1 reported is otherwise fixed.

**Setup**
- I reviewed `/tmp/h35b/snap4` with its CLI and `PTS_HOME=/tmp/h35b/snap4`, against `/tmp/h35b/snap3`, the base `/tmp/h35b/wt-base` and Node.
- I edited nothing in the repository. Mutants were built only through `go test/build -overlay` and copied PTS_HOMEs under `/tmp/h35b/review2/homes`.
- The parent's suite4 was still running throughout, so I kept to at most 2 heavy jobs.
- Archive: `/tmp/h35b/review2/h35b-review2-probes.tar.gz`, with an `INDEX.txt`. It holds no binaries and no keys, and `pgrep -x p` is empty.

## Round-1 fixes, verified

- **R1-F1 (typed builders) is fixed.** In strict+partial mode:
  - `typed_map_results.pts`, `spread_concat.pts` and `map_literals.pts` exit 0;
  - `api_sweep_typed.pts` reports only line 48 (a `reduce` whose callback pushes, which is a change by the program);
  - a new probe, `constructions_extra.pts`, covers object rest, `Object.keys/values/entries` of a shape, `Array.from(set/map, f)`, `new Set(gen())`, `Object.fromEntries(map)`, `[...m]`, `Float64Array.from(…, f)`, `flat`, `Array.of`, typed and dynamic rest calls and regexp split. It reports only `new Array(3).fill(7)`, which is a change by design.
  - I checked every `&ir.Func{` in `pts/lower`. The `mapHelper` callers return either void or arrays, and the string helpers return strings the runtime publishes.
- **The new IR wrapper is handled where it needs to be.** `ir.Published` is handled wherever `RestArgs` is (`walk`, `print`, `effectsOf`, `expr`). The pattern matches that now see it wrapped (`trustsDynamic`, `isNewArray`) only become more conservative.
- **R1-F3 (barrier before later operands) is fixed for `this`.** `arg_order_escape2.pts` and `this_alias_escape.pts` exit 0. `escape_operand_kinds.pts` (method receiver, a call with `this` first, `new`, array and object literals, field and global stores) exits 0, and its emitted C puts every barrier after the operand's store. `ctor_method_escape.pts` still exits 70; that case is now in D-226's Known gaps.
- **R1-F4 (runtime makers) is fixed.** The barriers are present and in the right order in `fs.c` (`finish` and `rm_directory_error`), `fetch.c` `fail_system`, `pts_value_values` and the JSON reviver. Dropping them (`homes/r1f4-drops`) makes `TestConstructionsReachOtherThreadsWhole` fail on `constructions_runtime` (through `pts_value_values`). `r1f4_runtime.pts` reports the reviver and `readdirSync`.
- **Mutation checks of the strict test:**

  | Mutant | Result |
  |---|---|
  | `nopublished` | fails `constructions_typed` |
  | `noappended` | fails `tuples_spreads`, `constructions_runtime` and `constructions_typed` |
  | `noreorder` | fails `escaping_this` |
  | `norest` | survives (see R2-F3) |

- **x86-64 cost.** The typed-builder sweep compiles to identical x86 code on snap3 and snap4 (154 functions, 0 changed). Callgrind instruction counts, snap3 against snap4, with identical output:

  | Benchmark | snap3 | snap4 | Change |
  |---|---|---|---|
  | word_count | 8,788,622,420 | 8,789,264,345 | +0.007% |
  | word_freq | 7,961,635,102 | 7,961,555,330 | −0.001% |
  | sum_reduce | 28,249,668,799 | 28,249,668,799 | 0 |

- **No other regressions found.** Round-1 default-mode probes (string and collection sweeps, cross-loop kinds, runtime drops, release header, operand kinds) all exit 0. Ten probes print the same on base and snap4.
- **The strict test is sound for what it covers.** Strict+partial makes every store a fill and lets release stores clean nothing. The checker stops at the first report, and each `keep` stores its argument with no barrier in between. Its blind spots are memcpy-only fills and constructions the programs don't make (R2-F3).

---

### R2-F1 (high): the parallel library's builders return results filled after their maker's barrier

**Where.** These methods in `pts/lib/parallel.pts` build their result with lib code and return it with no barrier, so the R1-F1 fix (`Builds`, `Published`) doesn't reach them:
- `ParallelArray.map` (`new Array<U>(n)`, then `results[i] = …` in `pts_parallel_for`), `filter` and `flatMap` (pieces pushed, then joined by `push(...right)`), `sortBy`;
- `ParallelRange.map`, `filter` and `toArray`;
- `ParallelTypedArray.map` and `filter`.

**Why it matters.** Pieces stolen by workers are ordered: the worker's release and the caller's acquire cover them. The pieces the caller runs itself are not. That includes everything at `PTS_THREADS=1`, and in `pts_join`'s common case the right half the caller runs after `pop`'s fence. A thread that loads a published `.par.map` result on arm can see holes or zeros. These are the language's main parallel APIs. SPEC §4.1 ("as it was when its construction ended") and D-226 don't exclude them. `fs.pts`'s recursive `readTree` (pushes, then return) has the same shape.

**Repro:**
```
cd /tmp/h35b/review2/probes
python3 ../scripts/run.py --profile publication --threads 4 --check all,strict,partial par_builders.pts
# exit=70, "22 publication violation(s) in 15 place(s)"; reported lines 9-16 and 18:
#   nums.par.map, nums.par.filter, range(0,16).par.map, range(...).par.filter, ta.par.map,
#   nums.par.sortBy, nums.par.flatMap, range(0,8).par.toArray, ta.par.filter
# for example: new_buffer ← pts_array_of_length ← pts_fn_parallel_ParallelArray_map_number (pts:parallel:42),
#   "is held by the object (Array), which this store published", stored at pts_fn_keep (par_builders.pts:6)
# not reported: nums.par.toArray() (a slice) and join() (an array literal)
# --threads 1 gives the same lines (out/par_builders_1.txt)
```

**Suggested fix.**
- A `pts_publish()` after the caller's part of each operation, at the end of `operate()` in `pool.c`, once both halves and the combine have run. That covers every `pts_parallel_for` and `pts_parallel_reduce` result.
- And a lib-side barrier before the return of builders that fill after the operation: declare `pts_publish` as a lib intrinsic, or let lib methods carry `Builds`. `readTree` needs the same.
- Add `par_builders.pts`, marked `// publication: strict`, to the strict programs.

### R2-F2 (low): reading a reassigned `let` alias of `this` last changes JS evaluation order (a regression)

`thisEscapes` now treats any non-cell local declared as `this` as an alias, `let` included. `operands()` moves escaping reads to after the other operands, on the grounds that the value is "a read of a parameter, which nothing before it can change". That holds for `this`, but not for a `let` alias that a later operand reassigns.

**Repro:**
```
cd /tmp/h35b/review2/probes
python3 ../scripts/run.py --cli base  alias_reassigned2.pts   # second:1 first:2
python3 ../scripts/run.py --cli snap4 alias_reassigned2.pts   # first:1 first:2
cp alias_reassigned2.pts /tmp/x.mts && ~/.local/node/bin/node /tmp/x.mts   # second:1 first:2
```
The emitted C for `let self: Node = this; record(self, ((self = other), 1));` is
`double pts_tmp1 = ((self = …other…), 1.0); pts_class_Node *pts_tmp2 = (pts_publish(), self); pts_fn_record(pts_tmp2, pts_tmp1);`

**Fix.** Treat as aliases only locals that are never assigned after their declaration (`const`, or a `let` with no `Assign`). Alternatively, reorder only reads of `f.Params[0]` and leave alias reads in place, with the barrier still at the alias read.

### R2-F3 (low): gaps in the strict test's coverage

- `TestConstructionsReachOtherThreadsWhole` passes with the dynamic-call rest-array barrier removed (`overlay_norest.json`, `ok 7.4s`), because no strict program calls a rest-parameter function through `any`. My probe catches that mutant: `spread_concat.pts` in strict mode exits 70 at line 10 with it and 0 without it.
- No strict program covers the new `fs.c` and `fetch.c` barriers or enum literals. `readFileSync`'s memcpy fill can't be seen by the checker at all; that barrier rests on reading the code alone.
- The litmus's new runtime-made members (`text`, `doubled`, `table`, `record`) are stored into the `Payload` before `new`'s barrier. On arm hardware it therefore tests the `Payload` barrier, not the builders' own barriers.

**Fix:**
- add a dynamic rest call (`const dyn: any = rest; keep(dyn(1, 2, 3))`), an enum and a `readdirSync` to `constructions_runtime.pts`;
- in the litmus, also publish a callback method's result and a `new Map(entries)` directly through a slot.

**VERDICT: NOT READY**

## Round 3 (2026-10-26): READY

On snapshot `/tmp/h35b/snap5` (round 2's fixes). Probes: [h35b-review3-probes.tar.gz](h35b-review3-probes.tar.gz).

**Verdict: READY.** Every round-2 finding is fixed, and the new barriers are placed correctly, including on exception paths and in nested operations. One low-severity gap is left: two library builders still change a new object after its last barrier (R3-F1).

**Setup**
- I reviewed `/tmp/h35b/snap5` with its CLI and `PTS_HOME=/tmp/h35b/snap5`, against snap4, the base `/tmp/h35b/wt-base` and Node.
- I edited nothing in the repository. Mutants were built only through `go test -overlay` and copied PTS_HOMEs under `/tmp/h35b/review3/homes`.
- suite5 was still running throughout, so I kept to at most 2 heavy jobs.
- Archive: `/tmp/h35b/review3/h35b-review3-probes.tar.gz`, with an `INDEX.txt`. It holds no binaries and no keys, and `pgrep -x p` is empty.

## Round-2 fixes, verified

- **R2-F1 (parallel library builders) is fixed.**
  - `par_builders.pts` in strict+partial mode exits 0 on 1 and 4 threads (round 2: 15 places).
  - `operate()`'s barrier is placed correctly:
    - it sits after both halves, the combines and `pts_absorb_messages`, and every library entry point goes through `operate` (`pts_parallel_for`, `pts_parallel_for_each`, `pts_parallel_reduce`, `pts_parallel_join`);
    - with one thread, `pts_par_run` runs the leaf inline and then falls through to it;
    - a nested operation on a worker publishes on that worker before its result is stored;
    - a throwing piece skips the barrier, but then nothing built is returned. What a `parallel for` body stored is the program's own change.
  - `reduce_leaf` and `reduce_combine` now publish before storing into the slot.
- **The strict test catches the barriers it should.** `TestConstructionsReachOtherThreadsWhole` fails for each mutant:

  | Mutant | Fails |
  |---|---|
  | `norest` (the round-2 survivor) | `constructions_runtime` |
  | no barrier in `operate` | `parallel_builders` |
  | no barriers in `reduce_leaf` / `reduce_combine` | `parallel_builders` |

  Logs are in `out/mut_*.log`. Unmutated, the test passes (`ok 18.1s`).
- **R2-F2 (reassigned alias of `this`) is fixed.** `alias_reassigned2.pts` prints `second:1 first:2` on base and snap5. A new probe, `alias_destructure.pts` (reassigning by `[self] = …`, `{s: self2} = …` and a `this`-typed union), gives `second:1 second:2 second:3` on base, snap5 and Node.
- **R2-F3 (strict-test coverage) is fixed.** The norest mutant is caught. The litmus now publishes a map result and a `new Map` straight into a slot, and checks their contents.
- **Earlier probes on snap5 still pass.**
  - Every round-1 and round-2 probe gives the same results as before. In strict mode, `typed_map_results`, `spread_concat`, `map_literals` and `r1f4_runtime` exit 0. `api_sweep_typed` and `constructions_extra` each report only a change the program makes itself.
  - In default mode, the escape and operand-kind probes, the API sweeps, `cross_loop_kinds` and `release_hides_header` exit 0. `ctor_method_escape` still exits 70; that case is in D-226's Known gaps.
- **x86-64 cost.** Callgrind instruction counts, one thread, snap4 against snap5, with identical output:

  | Benchmark | snap4 | snap5 | Change |
  |---|---|---|---|
  | word_count (uses `.par`) | 8,788,504,013 | 8,788,623,014 | +0.001% |
  | fan_out | 3,169,202,878 | 3,169,202,878 | 0 |

- **Runtime and lowering.** Rescanning the runtime for fills after a maker's barrier finds nothing new. Every remaining hit is a failure path, an internal temporary, or covered by a later barrier.

### R3-F1 (low): two library builders change a new object after its last barrier

I rescanned `pts/lib` for functions that return a local after changing it, now including object literals and `new C()`. This found two the parent's scan (arrays, maps, sets) missed:
- **`path.parse`** (`path.pts:400–473`). It returns the literal `ret = { root: "", … }` after setting `root`, `base`, `name`, `ext` and `dir`. The last store, for example `ret.dir = "/"`, has no barrier after it. On arm, another thread may see `""` fields.
- **`ParallelRange.sort(compare)`** (`parallel.pts:657`). It is `new ParallelArray(this.toArray()).sort(compare)`, which sorts the fresh array in place after `toArray`'s barrier: `items.sort` on one thread, `pts_parallel_write_sorted` on more. The method returns no barrier after that. On arm, another thread may see the array unsorted or with duplicated elements.

**Repro:**
```
cd /tmp/h35b/review3/probes
python3 ../scripts/run.py --profile publication --threads 4 --check all,strict,partial lib_builders.pts
# exit=70; reports line 7 (range(0, 8).par.sort): toArray's buffer, ← pts_fn_parallel_ParallelRange_sort (pts:parallel:657)
# lines 9-10 (parse): an Object allocated at pts_fn_path_parse (pts:path:400), stored at keep (line 6)
# line 8 (range.par.sortBy) is clean, through operate's barrier
```

**Fix:**
- declare the intrinsic `pts_publish` in `path.pts` and call it before `return ret` in `parse` (both returns are fine: the early `return ret` is the bare literal, already published);
- in `ParallelRange.sort`, publish the result of `new ParallelArray(this.toArray()).sort(compare)` before returning it;
- add `range(…).par.sort` and `parse` to the strict programs.

**Not defects, for the record:**
- `AbortSignal.any` is reported by strict mode, because `#sources` is set after the signal is made. On ARMv8, the `#follow` lock's acquire CAS orders the sources array's release before the publishing store. A reader that misses `#sources` falls back to `[given]`, which is still correct. The report comes from the checker not modelling pts:sync locks.
- `AbortSignal.abort` is covered by the `Event` it constructs after settling.
- `assert`'s `failWith` sets `generatedMessage` after construction, on an error it then throws. A racing reader could see a stale `false`. This is a nit.

**VERDICT: READY**
