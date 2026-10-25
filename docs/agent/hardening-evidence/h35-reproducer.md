# H35 reproducer report

The `reproducer` (claude-opus-5-5, isolated worktree of `2baeba1d0`), 2026-10-26: its final message, as it returned it. Its archive: [h35-reproducer-probes.tar.gz](h35-reproducer-probes.tar.gz) (108 files: scripts, probes, aarch64 assembly, logs, `red-tests.patch`, `local-sync-yield.patch`, `experiment-ready-flags-acq-rel.patch`; no keys, no binaries). The parent's checks of its claims are in [h35.md](h35.md).

---

I wrote failing (RED) tests for the H35 gaps and ran the corpus cross-compiled for aarch64 under qemu, plus native runs with tools that model arm's behaviour. The corpus run found one real aarch64 bug: the runtime doesn't compile, because of `__builtin_ia32_pause()` in `sync.c`. Every program that finished gave the right output. The memory-ordering audit found two places that depend on x86's store order. One is confirmed by ThreadSanitizer. The other is confirmed only by the aarch64 disassembly plus the ARMv8 memory model, because I have no arm hardware. The Chase–Lev deque matches Lê et al. 2013 line by line. I committed nothing and ticked nothing.

## Setup

- **Compiler wrappers:** `/tmp/h35s/repro/x/cc-aarch64` runs `clang --target=aarch64-linux-gnu "$@"`. `/tmp/h35s/repro/x/cc-unsigned-char` runs `clang -funsigned-char "$@"`, which gives aarch64's `char` signedness at x86 speed.
- **Runner:** `/tmp/h35s/repro/run-cross-suite.sh` sets `PTS_CC`, its own `PTS_CACHE_DIR`, `QEMU_LD_PREFIX=/usr/aarch64-linux-gnu`, `GOMAXPROCS=4` and `PTS_REQUIRE_NODE=1`. It then runs `go test -json -count=1 -p 4 -parallel 4 -timeout 90m`.
- **Load:** I stayed at 4 jobs after `suite.exit` appeared and never ran two suites at once.

## 1. The corpus under qemu-aarch64

| Run | Exit | Result |
|---|---|---|
| `./pts/toolchain/ ./cmd/pts/ ./pts/driver/`, cross | 1 | driver 1054/0, cmd/pts 30/0, toolchain 3 pass / 26 leaf failures |
| `./pts/tests/`, cross | 1 | 2,699 pass, 401 skip, 16 leaf failures (native baseline: 2,729 / 401 / 0) |
| `./pts/tests/ ./pts/toolchain/`, native with `-funsigned-char` | 1 | tests 2,732 pass / 401 skip, toolchain 32/0; the only failures are my 2 TSan RED tests |
| All 21 runtime `*_test.c` in Release, cross (temporary probe, since removed) | 0 | 21/21, including pool, scheduler, sync and typed_array_race |
| `TestFetchHTTPSEveryLoop` alone, cross | 0 | pass in 115 s |

The number-formatting test (25,000 values compared with JavaScript) passes on aarch64.

### (a) Real aarch64 bugs

- **A1: the runtime doesn't compile for aarch64.**
  - Cause: `pts/runtime/src/sync.c:50` calls `__builtin_ia32_pause()`, so every program build fails.
  - Error: `sync.c:50:5: error: use of unknown builtin '__builtin_ia32_pause' [-Wimplicit-function-declaration]`.
  - Fix: `yield` on aarch64. The other x86 asm, `pts.h:2357` and `runtime.c:75`, already has a C fallback that works.
- **No wrong-output bugs.** Every program that finished matched Node or its `// expect:` block.
  - `char` signedness: compiling every runtime file with `-fsigned-char` and `-funsigned-char` changes code in only 8 files. All 167 affected sites compare against ASCII or are reached only for ASCII bytes. The full corpus with `-funsigned-char` agrees.
  - `long double` and 16-byte atomics are not used; `va_list` use is correct.
  - setjmp/longjmp: GCC's `-Wclobbered` for both targets flags only `pool.c:508/512`, which is harmless (the handler only rethrows). The volatile marking in cgen covers `x++` and compound assignment.
- **The memory-ordering bugs (M1 and M2 in section 2) are real aarch64 bugs that qemu can't show.** qemu-user on x86 runs with roughly x86 ordering, so these runs prove nothing about ordering.

### (b) qemu and local tooling limits

- **Sanitizer builds can't link (32 tests).** This clang has no aarch64 compiler-rt: `cannot find .../libclang_rt.asan-aarch64.a` and `...tsan-aarch64.a`. Affected:
  - `TestRuntimeUnitTestsPassUnderSanitizers` (21 subtests) and `TestThreadedRuntimeTestsPassUnderThreadSanitizer` (4).
  - `TestFetchUnderSanitizers/{fetch_abort,fetch_many}` and `TestFetchHTTPSUnderSanitizers/{fetch_tls,fetch_tls_parallel}`.
  - `TestHTTPServerLifecycle`, `TestHTTPSServerLifecycle`, `TestHTTPSServerFetchesItself`.
- **Emulation is slower than the 30 s program deadline (9 tests).** These failed with empty or cut-off stdout under load. Run alone with no deadline (`/tmp/h35s/repro/triage.sh`), each exits 0 and matches:

  | Test | Alone under qemu | Output |
  |---|---|---|
  | `text/cron_describer` | 17.5 s | matches Node |
  | `long_string_scans` (Run and RunMatchesNode) | 26.2 s | matches Node and `// expect:` |
  | `parallel_sort_all_or_nothing` (threads 1, 2, 4) | 27.4 s (threads=1 only) | matches `// expect:` |
  | `timers_refused_no_leak` (threads 2, 4) | 86.9 s (threads=2 only) | matches `// expect:` |
  | `TestFetchHTTPSEveryLoop` | 115 s | passes |

  I re-ran each program at one thread count only (the second column says which), not every thread count that timed out.

### (c) Harness assumptions about the host

- **C1:** `TestProgramDependsOnlyOnLibc` (`pts/toolchain/runtime_test.go:191`) allows only `/lib64/ld-linux-x86-64.so.2`. It will fail on an arm64 runner, where the loader is `/lib/ld-linux-aarch64.so.1`. Under qemu, `ldd` exits 1. Fix: allow both loaders, or read `PT_INTERP`/`DT_NEEDED` with `debug/elf`.
- **C2:** The 30 s `programTimeout` assumes native speed; only an emulated CI job would need it scaled.

## 2. Memory-ordering audit

### Chase–Lev deque (`pool.c`) against Lê et al. 2013, C11 version

| Operation | Paper | `pool.c` |
|---|---|---|
| push | `bottom` relaxed, `top` acquire, `array` relaxed, slot relaxed, `fence(release)` then `bottom` relaxed | same at 105–111; `bottom` stored with release at 112, which is equivalent for the thieves' acquire load |
| resize | store `array` with release | release at 99 |
| take | `b-1`, store `bottom` relaxed, `fence(seq_cst)`, load `top`, CAS seq_cst on the last job, restore `bottom` | identical at 117–131 |
| steal | `top` acquire, `fence(seq_cst)`, `bottom` acquire, `array` consume, slot relaxed, CAS seq_cst | identical at 138–146 (`array` acquire, which is stronger) |

- **Job publication is correct.** A job, which lives on the owner's stack, is published by the release on `bottom`. Completion uses `done` release (343) and acquire (204). Results are written before `done`.
- **Sleep and wake are correct.** It is the store-buffering pattern with seq_cst fences on both sides (156 and 209).
- **The rest is also correct by reading:** the scheduler (inboxes under mutexes; `loop_count` and `slots[]` published by seq_cst), promises (messages under the inbox lock, the `handled` flag seq_cst), `sync.c` (acquire CAS, release unlock), pts:sync's `Once`/`Mutex`/`Channel` (seq_cst cells and futex locks), timers and immediates (seq_cst flags), inline caches (relaxed, but checked against the object's own descriptor), frozen twins (release store, and readers follow an address dependency) and all `pthread_once`/`uv_once` initialisers.
- **Evidence for this part is reasoning only.** The existing TSan tests pass on x86 in your baseline, and the Release C tests pass under qemu. Note that, as far as I know, LLVM's TSan does not model standalone fences for happens-before, so it can't validate the fence-based parts. A lost wake-up would show up as a hang in stress runs, not as a report.

### M1: fresh objects published without release (D-130's known gap, ARCHITECTURE §4.13 rule 6). Confirmed structurally; the outcome is reasoning.

- **Where.** A new object is filled with plain stores, then its pointer is stored into a shared slot with a plain or relaxed store:
  - generated field stores (`PTS_FIELD_SET`), module globals and cells;
  - array element writes to existing indices (`array.c:52-65` via `pts_array_set:372`);
  - setting an existing map key (`map.c:335`);
  - `pts_array_of` (plain `memcpy`, `array->length`);
  - string append buffers (`string.c:83`, `memcpy` after the CAS claim).
- **Safe paths.** `push` publishes through the length release (`array.c:114`); a new map key publishes through `used` and the index release (`map.c:351`, `:209`).
- **Tool evidence.** The aarch64 assembly of `/tmp/h35s/repro/probes/race_publication.c` (in `race_publication.aarch64.s`) shows `bl pts_fn_Payload_constructor` and then `str x24, [x21, :lo12:pts_global_variable]`, with no `dmb` or `stlr` in between.
- **Interleaving.** Writer A runs the constructor `str`s, then `str p -> slot`. Reader B runs `ldr q <- slot`, then `ldr [q]` (the type) and `ldr [q,#off]`. ARMv8 lets A's stores become visible in any order. B can then see:
  - word 0 still bdwgc's free-list link (or 0) instead of the type, which makes `instanceof` or method dispatch go through garbage or crash;
  - zero fields;
  - a closure whose `code` is NULL (a jump to 0);
  - a string header without its length or bytes.

  That breaks the contract's "never garbage, never a crash".
- **Tests.** (i) TSan does flag these accesses, but they are plain by design (rule 3), so TSan only becomes a precise test if the contract itself changes. (ii) For hardware I wrote the litmus `pts/tests/stress/race_publication.pts`: 400k rounds at 2, 4 and 8 threads, with a 10-field object wider than a cache line, a string, a nested object and a closure, published through a field, an array element, a map value and a module variable. It passes natively and under qemu (exit 0 in both); it is meant for nightly ×100 runs on an arm runner.

### M2: ready flags (module-scope `X_ready`, early-cell `PTS_CELL_READY`). Confirmed by TSan on x86 and by the aarch64 disassembly.

- **Where.** `pts/cgen/cgen.go:468` emits `X_ready = true` and `:474` emits `PTS_CELL_READY(c) = true`, both after the value. `cgen.go:1015` and `:994` read them with plain loads.
- **aarch64 code.** `/tmp/h35s/repro/probes/ready_flag.aarch64.s`:
  - writer: `str x8,[x0,#8]` (limit = 42), `str x0,[pts_global_config]`, `strb w8,[pts_global_config_ready]`;
  - reader: `ldrb w8,[..._ready]; tbz`, then `ldr x0,[pts_global_config]`, then `ldr x9,[x0]`.
- **Interleaving.** This is the message-passing pattern with a control dependency only, which ARMv8 allows to reorder on both the store and the load side. The reader can see the flag set while the binding is still NULL and fault on `ldr x9,[x0]`, or see the object with `limit` still 0. There is no address dependency between the flag and the value, so the fix needs acquire on the reader as well as release on the writer.
- **Experimental fix.** I changed cgen to emit `__atomic_store_n(&X_ready, true, __ATOMIC_RELEASE)` and `pts_check_ready(__atomic_load_n(&X_ready, __ATOMIC_ACQUIRE), …)`, and the same for `PTS_CELL_READY`. The TSan test then passes. I reverted it; it is in `experiment-ready-flags-acq-rel.patch`. On x86 these are still plain `mov`s; on aarch64 they become `stlrb`/`ldarb`.

## 3. RED tests (all four in `red-tests.patch`, which applies cleanly to HEAD)

1. **`cmd/pts/cross_target_test.go`** (5 tests): `build --target aarch64-linux` produces an `EM_AARCH64` executable that runs with `PTS_THREADS=4` and prints the expected pool, stack-guard, number, Unicode and RegExp output. The file also tests `--target=aarch64-linux`, a host-target build, rejection of `sparc-solaris` (the error must list the targets) and of a missing value, and that `help build` documents the flag.
   - It skips without clang aarch64 or qemu; `PTS_REQUIRE_CROSS=1` turns the skip into a failure, like `PTS_REQUIRE_NODE`.
   - It belongs in cmd/pts because the flag is a CLI contract.
   - RED: all 5 fail with exit 2 and `pts: unknown option "--target" for build` (`logs/red-cli-target.log`).
2. **`pts/toolchain/cross_test.go`** `TestRuntimeBuildsAndRunsForAarch64`: builds the runtime through a `PTS_CC` wrapper, checks `EM_AARCH64`, and runs the program under qemu with 3 threads.
   - RED at HEAD: the `sync.c:50:5` error above, `--- FAIL (3.84s)`.
   - GREEN with `local-sync-yield.patch`: `--- PASS`.
3. **`pts/tests/memory_order_test.go`** `TestReadyFlagsPublishTheirValuesToOtherThreads` (module binding and early cell): built under the ThreadSanitize profile, run with `GC_DONT_GC=1 TSAN_OPTIONS=halt_on_error=1`.
   - RED: `WARNING: ThreadSanitizer: data race … Write of size 1 … by main thread: #0 pts_step_main … Previous read of size 1 … by thread T2: #0 pts_fn_limitWhenReady … Location is global 'pts_global_config_ready' of size 1`. The early-cell variant has the same race, between `pts_step_fn_round` and `pts_fn_round_read`.
   - GREEN with the experimental cgen patch: both pass in about 1.8 s (`logs/green-tsan-ready-flags-experiment.log`).
4. **`pts/tests/stress/race_publication.pts`**: the M1 litmus. It is not RED on x86 or qemu and is meant for arm hardware only.

## 4. CI on `ubuntu-24.04-arm`

- **Runner:** the repository is public, so the free arm runner is available.
- **Cache key (needs a fix):** `.github/workflows/pts.yml` keys its cache on `pts-${{ runner.os }}-…`, which is `Linux` on both architectures. The arm and x86 jobs would therefore share one key, and only one of them would ever save its cache. Add `runner.arch`.
- **clang:** `apt install clang` on arm64 noble should ship the aarch64 sanitizer runtimes, so the sanitizer tests should run there.
- **Go:** `setup-go`'s `go-install.ps1` handles arm64, but I couldn't confirm that pwsh is preinstalled on that image; check the first run's log.
- **Before arm CI can be green:** fix C1, and add an arm matrix entry to `pts-nightly.yml` as well.
- **Cross-compile job on x86:** install `gcc-aarch64-linux-gnu libc6-dev-arm64-cross qemu-user-static` and set `PTS_REQUIRE_CROSS=1`.

## Not checked

- Real arm hardware, so the M1 and M2 outcomes are inferred, not observed.
- Sanitizers on aarch64.
- The native `pts build` path on an arm64 host.
- Benchmarks on arm.
- herd7 isn't installed, so the ARMv8 model was not machine-checked.

## Recommended fixes

1. **`sync.c` pause:** `yield` (or `__builtin_arm_yield()`) on aarch64 and nothing on other architectures.
2. **`--target`:** add a target to the toolchain and put it in `cacheKey` explicitly. Today the key hashes `CC --version` with no flags, so if the target is passed as a flag to plain clang, x86 and aarch64 archives would collide. Pass the target to `CheckProgram` too. Host `ar` handled aarch64 objects in all these runs. When the sysroot is missing, give a clear error naming the packages.
3. **M2:** release and acquire on the ready flags, as in the experiment patch.
4. **M1 (your choice):**
   - (a) a store-store barrier (`dmb ishst`; on x86 only a compiler barrier, which also stops clang moving initialising stores past the publishing store) after each object is built, before it can escape, in generated code and in the runtime's allocators that fill objects;
   - (b) release stores at every heap pointer store.

   Readers can keep plain loads, relying on the address dependency. Update §4.13 rule 6.
5. **C1:** the `ldd` allowlist.
6. **Optional performance:** the aarch64 stack guard reaches TLS through the GOT and builds the frame before the check even on fib's base case. A fast path using local-exec TLS and a direct `sp` compare would fix that.
7. **CI:** the items in section 4.

**Side issue, not arm-specific:** TSan placed the accesses one `.pts` line later than the statement (it reported 5:106 and 14:27 for lines 4 and 13). A `#line N` directive followed by a `{` line pushes the C code onto N+1, so `Error.stack` line numbers may also be off by one in these cases.

## Files

All under `/tmp/h35s/repro/`:
- `red-tests.patch` (the four test files above, which are also untracked in the worktree)
- `local-sync-yield.patch`
- `experiment-ready-flags-acq-rel.patch`
- `h35-reproducer-probes.tar.gz` (108 files: scripts, probes, aarch64 assembly, logs; no keys, no binaries)
- `logs/` (the suite runs and focused RED/GREEN logs)
- `xcache/` (kept, about 32 MB of aarch64 archives)

No probe processes are left running.
