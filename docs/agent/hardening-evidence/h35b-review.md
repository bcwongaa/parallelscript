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
