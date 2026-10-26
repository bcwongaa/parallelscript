# H35b reproducer report

The `reproducer` (claude-opus-5-5, isolated worktree of `bdc769e35`), 2026-10-26: its final message, as it returned it. Its archive: [h35b-reproducer-probes.tar.gz](h35b-reproducer-probes.tar.gz) (scripts, probes, emitted and hand-edited C, logs; no binaries, no keys); its patch (`checker.patch`) is merged into H35b's change. The parent's checks of its claims are in [h35b.md](h35b.md).

---

The checker works, and HEAD is RED as expected. It reports unfenced publications for every construction kind I tested, for all four channels of the litmus, for all 15 stress programs and for 94 of 210 buildable sampled programs. It reports nothing for programs whose new objects stay private. A barrier placed by hand removes exactly the program-level reports. Overhead against Release is 3.7–8.4×. The TSan ready-flag test (C357) still fails at HEAD. The patch is at `/tmp/h35b/repro2/checker.patch` (35 files, +2026/−6) and applies cleanly to HEAD. I committed nothing and ticked nothing.

**Things you need to know before using it:**
- **Atomic stores are instrumented.** Your assumption that they aren't is wrong for clang 18. Relaxed, release and seq_cst `__atomic_store_n` all call `__sanitizer_cov_store8`. Only CAS and exchange (and memcpy/memset) are invisible. So the explicit hooks are needed for release semantics, not for visibility.
- **I changed your fill rule.** By default only a word's first store counts as a fill, not every store into a fresh object. Your rule is still available as `PTS_PUBLICATION_CHECK=strict`. The reason is in section 1.3.
- **I added three refinements:** stores made under pthread locks, stores into malloc's memory, and release stores the runtime announces are treated specially. Each removed a large false-positive class; details in 1.3.
- **`checker.patch` leaves out the two files from `red-tests.patch`** (`pts/tests/memory_order_test.go`, `pts/tests/stress/race_publication.pts`). Apply `red-tests.patch` too: `TestNewObjectsReachOtherThreadsFilled` needs the litmus.

## Setup

- I symlinked the six `pts/third_party` dirs to the main tree. `GOMAXPROCS=4 make build` exited 0.
- The scratch dir is `/tmp/h35b/repro2/`.
- `/tmp/h35b/base-suite.done` existed (08:25) before my first heavy run. After that I used at most 3 parallel jobs and never ran a full suite.
- No probe processes are left: `pgrep -x p -a` printed nothing.

## 1. The publication checker

### 1.1 What clang 18.1.3 emits

Probe: `/tmp/h35b/repro2/cov/t.c` and `probe.sh`.

| `-fsanitize-coverage=` | Callbacks emitted |
|---|---|
| `trace-stores` alone | none |
| `func,trace-stores` | only `__sanitizer_cov_store{1,8,…}`, no pc-guard. This is what the profile uses. |
| `func,trace-stores,trace-pc-guard` | also `trace_pc_guard` and `trace_pc_guard_init` |

Per kind of store:
- Plain stores call the hook.
- Atomic stores (`__atomic_store_n` relaxed, release, seq_cst) also call `__sanitizer_cov_store8`.
- `__atomic_exchange`, `__atomic_compare_exchange`, struct copies and memcpy/memset: no callback.
- A function under `__attribute__((no_sanitize("coverage")))`: no callback. The checker's object file has 0 `__sanitizer_cov` relocations.

The instrumentation reaches 3,550 stores in the runtime archive (`logs/archive-stores.txt`). `shared_memory.o` alone has 30, all of them relaxed atomic stores.

### 1.2 What the patch adds

**The profile (`pts/toolchain/toolchain.go`).**
- `PublicationCheck` (String `"publication"`) compiles with `-O1 -g -ffp-contract=off -fno-math-errno -fno-omit-frame-pointer -fsanitize-coverage=func,trace-stores -DPTS_PUBLICATION_CHECK`. These flags apply only to the runtime and the program; the vendored libraries keep `vendoredFlags`.
- A new `Profile.linkFlags()`, used only by `CompileProgram`, adds `-Wl,--wrap=` for `GC_malloc`, `GC_malloc_atomic`, `GC_gcj_malloc`, `GC_malloc_uncollectable`, `GC_realloc`, `GC_free`, `pthread_mutex_lock`, `pthread_mutex_trylock`, `pthread_mutex_unlock` and `pthread_once`.
- The macros expand to those symbols here (`clang -E`, and `gc_gcj.h:103`): `GC_MALLOC`→`GC_malloc`, `GC_MALLOC_ATOMIC`→`GC_malloc_atomic`, `GC_GCJ_MALLOC`→`GC_gcj_malloc`, `GC_MALLOC_UNCOLLECTABLE`→`GC_malloc_uncollectable`, `GC_REALLOC`→`GC_realloc`, `GC_FREE`→`GC_free`.
- bdwgc is one translation unit, so its internal calls are not wrapped.

**The whole-suite mode.**
- `toolchain.BuildProfile()` reads `PTS_BUILD_PROFILE` (release, sanitize, tsan or publication), and `driver.build` uses it.
- `PTS_BUILD_PROFILE=publication go test ./pts/...` therefore runs every program any test builds under the checker. That covers runPts, the http/examples/console tests and `pts test`.
- `pts/tests/run_test.go` multiplies `programTimeout` by 10 when the variable is set.

**The checker's code.**
- `pts/runtime/src/publication_check.c` compiles to an empty unit without `-DPTS_PUBLICATION_CHECK`. With it, the whole file is under `#pragma clang attribute push(no_sanitize("coverage"))`.
- It compiles cleanly with `-Wall -Wextra`, for x86-64 and for `--target=aarch64-linux-gnu` (`scripts/compile-checker*.sh`).

**`pts.h`.**
- Declares `pts_publication_barrier_hook()`.
- Adds `PTS_PUBLICATION_STORE(addr)` and `PTS_PUBLICATION_RELEASE(addr, also)`, which are no-ops normally. I avoided the `PTS_CHECK_` prefix because it would collide with `pts_check` kinds.
- I did **not** define `PTS_PUBLISH`; that's yours. Under `-DPTS_PUBLICATION_CHECK` it must also call the hook. A suggested definition: `#define PTS_PUBLISH() do { PTS_PUBLISH_FENCE(); pts_publication_barrier_hook(); } while (0)`, where the fence is `asm volatile("dmb ishst":::"memory")` on aarch64 and `asm volatile("":::"memory")` elsewhere.

**Hooked sites** (`PTS_PUBLICATION_RELEASE`, right after each release store; line numbers are after the patch):
- `array.c:112–120`: `publish_buffer`, and `publish_length` with `also` = the buffer.
- `map.c`: 210 (`index_entry`), 234 and 236 (`replace`: `old->next`, `map->table`), 355 (`used`), 358 (`size`).
- `generator.c`: 411 (`next_entry`), 498 and 519 (`executing=false`).
- `error.c:40` (`executing=false` in `pts_throw`).
- `object.c:726` (frozen twin type).
- `regexp_object.c:73` (literal cache).
- `pool.c`: 90 and 101 (the deque `array`).

**Not hooked, and why:**
- The map `size`/`hash` release stores on delete and clear: no pointer is published.
- `sync.c` unlock: treating it as a barrier would hide the case where an unlock doesn't order a later plain publication.
- The `promise.c` `handled` flag.

**CAS and exchange.** Every one in the runtime is at convert.c:225 (malloc'd memory), array.c:413 (static type), string.c:80 (a byte claim), pool.c:128/146/519, generator.c:531, sync.c, loop.c, promise.c:315/323, http.c:1202, scheduler.c:259 or console.c:371. None stores a fresh GC pointer, so `PTS_PUBLICATION_STORE` has no runtime user today; the C cases test it.

**pts_value.** Heap pointers are raw (`0x0000_PPPP_PPPP_PPPP`), and numbers carry a +2^49 offset, so decoding a value as a raw pointer already covers the pts_value case.

### 1.3 Semantics, per thread

**The table and the barrier.**
- A 32K-entry ring plus a 64K open-addressing hash (backward-shift delete) holds the fresh objects. Each entry has base, size, a dirty epoch, a written-word bitmap and 4 allocation PCs (from frame pointers).
- An allocation adds an entry that is dirty.
- The barrier hook makes everything clean in O(1), because it increments the thread's epoch.

**A store before it happens** (the callback has only the address):
- Into the thread's stack or static TLS (`dl_iterate_phdr` `dlpi_tls_data`): ignored.
- Into a fresh object: a fill.
- Anywhere else: recorded as pending, and checked at the thread's next event (store, allocation, barrier, lock, thread exit via a pthread key, exit via atexit) by reading the value at the address.

**Change from your spec 1: what counts as a fill.**
- By default a fill is the first store into a word that holds zero; strict mode counts every store.
- Evidence: I fenced an object literal by hand in emitted C, then changed `o.x` and published it (`probes/green/mutation.*`). The default mode reports nothing at `mutation.pts:10`. Strict mode reports `pts_fn_publish (mutation.pts:10) stored into global pts_global_shared+0`.
- Your design puts no barrier after a mutation, so strict mode would flag every "construct, change, publish". The C case `mutation` pins both behaviours.
- To make the zero test reliable, the wrap zeroes `GC_malloc_atomic` memory, which bdwgc doesn't.

**Publication.**
- A pending store publishes a fresh object if its address is in static data, the collected heap (outside fresh objects) or another live thread's stack.
- If the object is dirty, that is a violation. Otherwise the object becomes shared and leaves the table.
- Sharing is transitive: the shared object's words are scanned, and any fresh object it holds is shared too, or reported if dirty ("is held by the object …").

**Change from your spec 2: three new rules.**
- **Locks.** A store made while the thread holds a pthread mutex, or runs a `pthread_once` routine, is not a violation.
  - The case: runtime inboxes, the once-made loops and pools.
  - Pinned by C cases `locked` (exit 0) and `after_unlock` (reported).
- **malloc's memory** counts as private; `malloc` option to count it as shared.
  - The case: inspect.c's `seen` list (`push`, inspect.c:64) was 223 of the early reports.
  - Pinned by `malloc_scratch`.
- **Release hooks.** `PTS_PUBLICATION_RELEASE` makes the containing object, the stored value's object and `also` clean, and publishes the value without a violation. The readers use acquire (`pts.h:1315,1322`).
  - With `partial`, it cleans nothing. Then "push into a new container, then a plain publish" is reported. Pinned by C case `pushed`.
  - This is exactly what the runtime auditor flags: a reader can see a prefix of the container, never garbage. Decide whether runtime builders such as split, JSON.parse or keys need `PTS_PUBLISH` anyway.

**Uncollectable allocations** are tracked, and the ring never evicts them. A thread's own structures stored only in TLS, such as the microtask queue and loop state, therefore stay private. This removed the `enqueue` and `remember_step` false positives.

**After a collection** (`GC_get_gc_no` changed), entries the collection didn't mark are dropped, under `GC_alloc_lock` with `GC_is_marked`. Another thread may get a dead object's memory, and a stale entry would then report a false violation.

**Reports and options.**
- Each report prints the object (address, size, type name from word 0), its allocation stack, and the slot (global symbol via `backtrace_syminfo`, heap word + type, another thread's stack, or malloc's memory). It also prints the store PCs, symbolized with libbacktrace through `#line` to `.pts` lines.
- The default is to stop at the first violation with exit code **70**.
- `PTS_PUBLICATION_CHECK` options:
  - `all`: report each (store PC, allocation site) place once, then exit 70 at the end;
  - `count`: count only;
  - `stats`;
  - `strict`;
  - `malloc`;
  - `partial`.

### 1.4 Tests and RED evidence

**Checker unit tests (`pts/toolchain`, independent of the fix).** `go test ./pts/toolchain/ -count=1` → `ok 55.4s`.
- `TestPublicationCheckerReportsOnlyObjectsPublishedBeforeTheirFill` builds `pts/runtime/tests/publication_cases.c`. All 16 cases pass:

  | Outcome | Cases |
  |---|---|
  | Exit 0 | `fenced`, `private` (prints 3), `mutation`, `release`, `locked`, `malloc_scratch` (prints 2), `pushed` |
  | Exit 70, slot and allocation site named | `unfenced`, `held_dirty`, `mutation/strict`, `after_unlock`, `compare_and_swap`, `thread_end`, `malloc_scratch/malloc`, `other_stack`, `pushed/partial` |

- Example report (`held_dirty`):
  ```
  pts publication violation: a new object reached memory other threads can read with words written after the last publication barrier
    object 0x73a92d87dfc0 (24 bytes), allocated at
        new_box (pts/runtime/tests/publication_cases.c:21)
        inlined into held_dirty (pts/runtime/tests/publication_cases.c:49)
    is held by the object 0x73a92d87dfe0, which this store published
    stored into global published+0, at
        held_dirty (pts/runtime/tests/publication_cases.c:53)
  ```
- `TestPublicationWrapsCoverEveryAllocatorTheRuntimeCalls` runs `nm --undefined-only` on `libptsrt.a`. I mutation-checked it: removing `GC_realloc` from the wraps made it fail with `the runtime calls GC_realloc, which the publication checker doesn't wrap`.
- `TestBuildProfileNamesAProfileOrReleaseWhenUnset` passes.

**The fix's test (`pts/tests`).** `GOMAXPROCS=4 go test ./pts/tests/ -run TestNewObjectsReachOtherThreadsFilled -count=1 -v -parallel 4` → **exit 1**.
- It runs 46 programs × threads 1/2/4:
  - 18 programs in `publication/`: 17 construction kinds plus `private_objects`;
  - `stress/race_publication.pts`;
  - 27 sampled `run/`, `stress/` and corpus programs.
- Result: **105 leaf FAIL / 30 PASS / 1 SKIP** (`data/mini_query_engine` has a `// skip:` line).
- The PASSes are `private_objects` plus 9 samples that publish nothing.
- Example failure:
  ```
  publication_test.go:60: assertion failed: strings.Contains(got.stderr, publicationReport) is true: stderr:
      pts publication violation: … object 0x7a2a47448fc0 (32 bytes, Object), allocated at
          pts_object_new (pts/runtime/src/object.c:23)
          pts_fn_make (publication/object_literal.pts:10)
      stored into global pts_global_shared+0, at
          pts_step_main (publication/object_literal.pts:18)
  ```

**First violation per kind** (default mode, `PTS_THREADS=2`; `logs/kinds-head.txt`):

| Program | Object, made at | Stored into, at |
|---|---|---|
| object_literal | Object, `pts_fn_make` object_literal.pts:10 | `pts_global_shared`, :18 |
| new_instance | `holder` literal, :17 | `pts_global_holder`, :17 |
| closure | closure, `pts_new_closure_makeCounter_arrow` | `pts_global_shared`, closure.pts:18 |
| bound_functions | bound closure, `pts_new_closure_build_bound` | `pts_global_addTen`, :19 |
| typed_arrays | Float64Array, `new_view` typed_array.c:67 | `pts_global_floats`, :4 |
| maps_sets | Map, `pts_map_new` map.c:249 | `pts_global_byName`, :5 |
| generators / json_parse | string, `pts_string_concat` / `pts_string_from_utf8` | `pts_global_gen` :16 / `pts_global_parsed` :11 |
| async_promises | Object made after an await, :13 | `pts_global_made`, :16 |
| channels | Channel instance, :8 | `pts_global_jobs`, :8 |
| arrays, errors, escaping_this, strings, subclass, regexp, litmus | Array, `pts_array_of` array.c:151 | the module binding (litmus: `pts_global_LABELS`, race_publication.pts:12) |
| private_objects | no violation | |

**The litmus in `all` mode at 4 threads** reports all four publication channels:
- the field (`pts_field_set_slow` object.c:323, and inline at :72);
- the array element (`pts_array_set`, :73);
- the map value (`pts_map_set` map.c:335/338, :74);
- the module variable (`pts_fn_setVariable`, :59);
- plus the nested `Inner` and the closure "held by" each `Payload`.

**Breadth.** `sweep.py` over 216 programs (every 8th run test, every 5th corpus program, all of `stress/`):
- 94 report violations (exit 70), including all 15 stress programs;
- 110 report nothing;
- 6 exit 1 as they expect;
- 6 don't build (corpus programs with `skip: unsupported: PTS1001`).

**Whole-suite mode check.** `PTS_BUILD_PROFILE=publication go test ./pts/tests/ -run TestRun/classes` → exit 1 (TestRun/classes, classes_integers, …; TestRunMatchesNode/classes, …).

**No false positives.**
- `publication/private_objects.pts` covers literals, subclasses, closures, bind, maps, sets, tuples, spreads, JSON.parse, RegExp, errors, typed arrays and generators, all kept private.
- At HEAD: exit 0, `18006 allocations, 93644 stores into the heap or static data, 64203 fills, 2 publications`. `TestPublicationCheckerAcceptsObjectsNoOtherThreadSees` passes.
- binary_trees: 68,332,272 allocations, 0 publications, 0 reports.

**Barrier placed by hand.**
- The C cases `fenced` (exit 0) and `unfenced` (exit 70) are the simple pass/fail pair.
- In emitted C I added `pts_publication_barrier_hook();` before both `pts_tmpN; })` of object_literal's literal (`probes/green/object_literal.fenced.c`).
- Its reports dropped from 7 places to 4. Both object_literal.pts places disappeared; only promise.c's remain.

### 1.5 Overhead

Median of 3 interleaved runs, 1 thread, `count,stats`:

| Program | Release (s) | Checked (s) | Ratio |
|---|---|---|---|
| binary_trees | 1.65 | 11.31 | 6.8× |
| json | 1.24 | 4.53 | 3.7× |
| string_building | 1.25 | 5.99 | 4.8× |
| nbody | 1.00 | 8.40 | 8.4× |
| word_freq | 1.69 | 6.71 | 4.0× |
| race_publication | 0.12 | 0.73 | 6.2× |
| race_maps | 0.08 | 0.33 | 4.3× |

The GC-heavy stress programs at 4 threads ran 2.4–6.1×, with the same output as Release.

### 1.6 What the checker can't see

- **Stores clang doesn't make:** memcpy, memset, inline asm, the vendored libraries, and CAS/exchange unless announced. Fills made after a barrier by memcpy are invisible.
- **A store's value is read at the next event.** If uninstrumented code frees or overwrites the slot first, the check sees the new contents. The `malloc_scratch` case needed an extra store before `free` for this reason.
- **The reader's side:** for example, whether readers really acquire `length`.
- **Ownership protocols.** A promise's reactions, a loop's timer heap and a generator frame are owned by one loop, but the checker sees them as shared heap.
- **Forgotten objects:** collectable objects past the ring's 32K newest, and objects a collection didn't mark.
- **Store sizes:** 1–4-byte stores are never value-checked, and two sub-word fields sharing one word count as one.
- **Errors in the program-exit path:** at exit, exit code 70 replaces the program's own code.

## 2. Audit for barrier placement

Line numbers are for the tree with `checker.patch` applied. In array.c, lines after 118 are +6 against HEAD; in map.c, up to +5. Three read-only auditors produced the tables below. I verified samples against the source; all matched: string.c:12–25, integer.c:15–31, promise.c:143–148, error.c:404–406, loop.c:237–255, closures.go:184–199, dynamic_calls.go:192–197, generators.go:138–168, cgen.go:440–449 and 652–662, abort.pts:315–333 and 405–426, http.pts:238–262. I also cross-checked (a) against the makers the checker actually caught.

### (a) Runtime makers

Legend:
- **PUB**: put `PTS_PUBLISH` before the return or store.
- **inc**: callers fill the result, so the barrier goes in the caller.
- **temp**: never leaves the thread.
- **mutex/once**: handed over under a lock or pthread_once.

| File | PUB | inc / other |
|---|---|---|
| object.c, integer.c, date.c | `pts_cell_new` :31, `pts_cell_copy` :40, `pts_value_from_i64` integer.c:15 and `_u64` :25 (atomic, unzeroed), `new_date` date.c:190 | `pts_object_new` :14 inc |
| string.c and friends | `appended` :73 / `pts_string_concat` :260 (:271, :286), `pts_string_from_utf8` :122, `_from_ascii` :131, `_from_utf8_lossy` :179, `string_of_unit` sm:143, `units_between` :166, `repeated` :193, `split_bytes` / `pts_string_split` :602/:618, `to_well_formed` :741, `string_of_units` :760, `convert_ascii` string_case.c:131, `convert_case` :152 | `pts_string_alloc` :12, `buffered_string` :37, `new_buffer` :52 inc (alloc is atomic, so a reader can see garbage `byte_len`/`bytes`) |
| array.c | `pts_array_of` :149, `new_array` :562, `copy_as_read` :581, `flat` :1249, `from_string` :1315, `with` :1334, `of_length` :1352, `keys` :1362 | `new_buffer` :86 inc; `with_room`, `unshift`, `splice` buffers correct (release + acquire) |
| map.c | `pts_map_new` :248, `pts_map_copy` :254, `pts_set_of_array` :263 | `new_table` :165 inc; `replace` correct |
| typed_array.c | `new_buffer` :55, `new_view` :66, `arraybuffer_slice` :88, `from_array` :154, `from_typed` :163, `slice` :215, `to_reversed` :258, `to_sorted` :330, `with` :334 | |
| generator.c | `items_iterator_new` :130, `pts_iterator_of` :204, `iteration_rest` :360, `map_iterator_new` :440, `map_iterator_remaining` :455, `iterator_to_array` :675 | `pts_generator_new` :19 inc (cgen fills the frame) |
| promise.c | `new_promise` :143 (other loops read `type`/`home` plainly), `promise_resolve` :291, `resolving_functions` :342, `pts_async_start` :590, `promise_construct` :598, `promise_finally` :632, `promise_rejected` :669, `record_of` :743, `element_done` :772, `combine` :858, `pts_async_generator_new` :924, `from_sync_resume` :1031, `async_iterator_open` :1049, `async_iterator_resume` :1066 | post / run_task / then_with / run_thenable reactions: mutex or loop-local; settle :185 and resolve :248 temp |
| error.c | `aggregate_error_new` :336, `system_error_new` :342, `coded_error_new` :353, `pts_error_new` :386, before `pts_error_get_stack` :406 (a lazy store into a shared error) | `capture_frames` :227 covered by the error's barrier |
| regexp | `regexp_new` ro:52 (also covers `pts_regex_compile`), `to_string` :124, `match_array_at` :218, `string_match` rs:75, `split_regexp` :167, `replace_matches` :378, `replacer_arguments` :397 | |
| dynamic, convert, json | `bind_method` dyn:307, dm `array_concat` :458 / `map` :617 / `filter` :631 / `flat_map` :649 / `to_sorted`, `to_reversed`, `to_spliced` :758–762 / `function_bind` :885 / `own_keys` :1210 / `values`, `entries` / `value_iterable` :1393 / `value_to_array` :1437; one PUB at the end of `convert` convert.c:971 (or try_convert :1119, `_as_is` :1128, `convert_fresh` :1157); `parse_array` / `parse_object` json.c:347/:371, or once at the end of `pts_json_parse` :433 | |
| I/O | fs.c `finish` :287, `rm_directory_error` :523; http.c `parse_head` :368 (before calling the program :530); fetch.c `respond` :325, `fail_system` :212; process.c `argv` :54, `environ` :66; loop.c `pts_timer_set` :237, `pts_immediate_set` :379 | servers, fetch roots, pool and scheduler structures: lock, once or release, all correct |

**Empirical cross-check.** These are all the runtime makers whose objects the checker caught at HEAD (`logs/sweep-sites.txt`); every one is in the table:
- arrays: `pts_array_of` (101 places), `pts_array_of_length`, `new_array`;
- strings: `pts_string_concat` (54), `pts_string_from_utf8`, `pts_string_from_ascii`, `pts_string_from_utf8_lossy`, `repeated`, `string_of_units`, `convert_ascii`, `buffered_string`;
- typed arrays: `pts_arraybuffer_new`, `new_view`, `new_typed_array`;
- promises: `new_promise`, `new_function`, `async_suspended`, `pts_async_iterator_resume`, `then_with`, `run_task`, `async_generator_step`, `run_thenable`, `resolve`, `settle`, `pts_queue_microtask`;
- maps and iterators: `pts_map_new`, `pts_map_iterator_new`, `items_iterator_new`, `pts_iterator_of`, `pts_generator_new`;
- conversions: `copy_object`, `convert_tuple`;
- errors and regexps: `capture_frames`, `pts_error_new`, `regexp_new`, `pts_regex_compile`;
- timers and cells: `pts_timer_set`, `schedule`, `pts_immediate_set`, `pts_cell_copy`;
- dates: `new_date`.

**Shared caches and tables (Task B).** All are correct today except three:
- **Correct:**
  - regexp literal cache (release / acquire);
  - `union_table` (CAS acq_rel / acquire);
  - frozen twins (lock, then release + address dependency);
  - frozen mark table, bound-method descriptors, HTTP servers, fetch table, console labels (all under a lock);
  - function-name table (written before any thread);
  - libbacktrace, PCRE2 and TLS setup (once);
  - loop slots and `loop_count` (once, pthread_create, seq_cst);
  - deques and `deque_count` (release / acquire);
  - Chase–Lev buffers and jobs;
  - site caches (relaxed, only compared).
- **Wrong today:**
  - timer/immediate fields read by other threads (loop.c:244–251, 386–389);
  - promise and async-generator `home` (promise.c:147, :926);
  - the error stack cache (error.c:406).

  All three need the PUB rows above.

### (b) Constructions in generated code (`pts/cgen`)

| # | cgen | Builds | Barrier goes |
|---|---|---|---|
| 1 | classes.go:556–572 `newObject` | object literals, tuples, rest objects, spreads, yield snapshots | before the final `tmp; })` at :570, after any `pts_object_take_order` |
| 2 | classes.go:719–730 `newInstance` | `new C()`, including builtin externs (`pts_error_init`) | between the constructor call and `tmp; })` in the :728 format |
| 3 | closures.go:184–199 `closureConstructor` | every capturing closure, bind, method values, parallel tasks | before `return &closure->closure;` (:197) |
| 4 | dynamic_calls.go:192–197 | D-075 adapters | before `return &adapter->closure;` (:196) |
| 5–7 | generators.go:126–168 | generator / async / async-generator frames | before :166 / :162 / :164 |
| 8 | closures.go:246 `cellAllocation` (+ cgen.go:443–447, 654–661, generators.go:147) | cells | a barrier in `pts_cell_new`, or after `*cell = init`. In generator step functions a fresh cell is stored into the frame before its init; the claim protocol protects it. |
| 9 | cgen.go:466–469 | module bindings, static fields, class markers | between `X = init` and `X_ready = true` (C357) |

The runtime builds the rest on generated code's behalf: `pts_array_of`, array spread and concat (arrays.go:169–182), Map/Set, record and enum chains, the `Map.of` lowering helper (cgen only sees a `return map`), rest arrays in dynamic entries, strings, i64 boxing, conversions, promises.

**Where `this` escapes inside a constructor.** These are the IR forms cgen would have to recognise (cgen file:line):
- `Call.Args` (cgen.go:909–921), `VirtualCall`, `InterfaceCall`, `InstanceCall` (classes.go:931–1011);
- `CallValue`, `CallBuiltin`, `ArrayCall` (arrays.go:30–118), `MapCall`, `New.Args`, `FamilyCall`, `Throw`;
- `SetField.Value`, `FamilySetField`, `NewObject.Values`, `NewArray.Elems`;
- `LocalDecl.Init`, `Assign`, `Bind`;
- `MakeClosure` with `this` among its captures.

Three things to watch in the `this` rule:
1. **Evaluation order.** C leaves argument order unspecified, so the barrier must come after the operands are evaluated (the `operands` prelude, cgen.go:1357).
2. **Aliases.** In `const self = this; f = closure(self)`, `this` escapes through `self`.
3. **Late stores.** A barrier at capture time doesn't order stores made between the capture and a later call of that closure.

### (c) Flag-then-value and lazy-initialisation patterns

| Pattern | Verdict |
|---|---|
| Module `_ready` and `PTS_CELL_READY`, including static fields and class markers (cgen.go:467–474, readers :994/:1015/classes.go:722) | **ARM64 BUG (C357)**, covered by the planned fix. `pts_check_ready` takes the flag by value, so the acquire must be an `__atomic_load_n` at the use sites. |
| Reassigning a global or early cell after its declaration (cgen.go:977–982, :994) | **ARM64 BUG, C356 class.** The flag's release covers only the declaration; the value's construction barrier covers this. |
| `pts_cell_copy`'s relaxed copy of the ready flag (cgen.go:570) | Safe only through how the copy reaches other threads (a new closure, a message). It will not pair with the new release. |
| **AbortSignal `#reason` then `aborted`** (abort.pts:416–418, plain; readers without the lock at 332–333, 356–357, 315, http.pts:613/647/650) | **ARM64 BUG, not covered by the planned fixes.** A reader can see `aborted === true` with `#reason` undefined and throw `undefined`. The comment at 416 assumes x86 order. |
| Request's lazy `#signal` (http.pts:245 fast path, :251–257) | Double-checked init. It is fixed by the C356 construction barrier plus the reader's address dependency. |
| Imported-module init, enums, `pts_static_*`, `pts_site` caches, the regex literal cache | SAFE |
| Runtime: slots/`loop_count`, `awake`/`finished`, deques/`deque_count`, `pool_threads`, `join_job` `done`, `cancelled`/`first`, generator `executing`, timer `active`, promise `handled`/`done`, once-made state, `union_table`, frozen twins, lock-guarded tables, `group_indentation`, server `stopped`, array/map release/acquire | SAFE |
| Load hints, the map-iterator position, `promise_state` read from another loop | BENIGN (stale, never garbage) |
| Library `Once` (seq_cst AtomicInt), `Mutex`/`Channel` (lock), parallel `done.found`, library module bindings (written before threads exist) | SAFE |

## 3. The existing RED test at HEAD

`GOMAXPROCS=4 go test ./pts/tests/ -run TestReadyFlagsPublishTheirValuesToOtherThreads -count=1 -v -timeout 20m` → **exit 1**. Both `module_binding.pts` (1.69 s) and `early_cell.pts` (1.67 s) **FAIL**:
- `early_cell`: `WARNING: ThreadSanitizer: data race … Write of size 1 at 0x7ffff6d95ff8 by main thread: #0 pts_step_fn_round …early_cell.pts:16:37 … Previous read of size 1 … by thread T2: #0 pts_fn_round_read …early_cell.pts:11:89`.
- `module_binding`: `Write of size 1 at 0x555556b59ee0 by main thread: #0 pts_step_main …module_binding.pts:14:27 … Previous read … by thread T2: #0 pts_fn_limitWhenReady …:5:106 … Location is global 'pts_global_config_ready' of size 1`.

Full log: `logs/red-tsan-ready-flags.log`.

## What remains after the fix

The checker will probably still report a few runtime places whose memory is owned by one loop. At HEAD these were:
- `add_reaction` (stores into a Promise);
- `settle` promise.c:181;
- `place` loop.c:107 (the timer heap, once it has been evicted from the ring);
- `queue_request` promise.c:982;
- `set_step` generator.c:69.

Most will go away once the runtime makers have their barriers, because the stored objects become clean. A barrier before each of those stores is cheap; the alternative is accepting them.

Files are in /tmp/h35b/repro2:
- checker.patch — the diff of everything I wrote, without the two red-tests.patch files
- h35b-reproducer-probes.tar.gz — 458 files: scripts, probes, emitted and hand-edited C, logs, the auditor's scratch .pts; no binaries, no keys
- DONE
