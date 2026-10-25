# H35a independent review

The `reviewer` (claude-opus-5-5), resumed between rounds. Each round's final message as it returned it, with the parent's resolution after it.

## Round 1 (2026-10-26): NOT READY

Reviewed: `/tmp/h35s/wt-x`, `822bcd1fe` plus the uncommitted change (23 files). Probes: [h35a-review1-probes.tar.gz](h35a-review1-probes.tar.gz) (60 files, no binaries, no keys).

---

VERDICT: NOT READY

The code does what D-225 says, and I found no defect in the target, stack-guard or `process.arch` code. Two things hold it back: the docs say less than the truth about what arm64 lacks before H35b (R1-F1), and the help text shows a `PTS_SYSROOT` value that breaks every cross build (R1-F2). The other findings are small test and comment fixes. R1-F8 is an older bug that exists on the base too; it is not an H35a defect.

All probes ran pinned to `taskset -c 6,7` against a CLI built from `/tmp/h35s/wt-x` (`PTS_HOME=/tmp/h35s/wt-x`, a private `PTS_CACHE_DIR`). The base CLI used for comparison was `/tmp/h35s/wt-ci/pts-bin/pts`. Scripts and logs are in `/tmp/h35s/review1/h35a-review1-probes.tar.gz` (60 files, no binaries, no keys). `scripts/env.sh` says how to rebuild the CLI. I did not edit the worktree: `git status` lists the same 23 entries as at the start.

### Findings

#### R1-F1 (medium) The docs understate the arm64 safety gap, and C357 is missing from them
- **Where:** `docs/SPEC.md:341` (§4.1), `docs/SPEC.md:59` (§2.2), `docs/GUIDE.md:146`, `README.md:21` and `:621`, `docs/ARCHITECTURE.md:420`.
- **What they say:** each describes C356 only as "a racing reader can see an object before its fields".
- **Why that misleads:** SPEC §4.1 is the "non-negotiable" rule that a racing program may see "stale or surprising values" but "must never crash". Read under that heading, the arm bullet sounds like a surprising value. The repo's own CONSOLIDATED entries say more:
  - C356: "the invariant's 'no crash' breaks" (a garbage type word, a closure whose code is NULL).
  - C357: a reader can see a ready flag and a NULL module binding, which faults.
- **C357 is missing:** the ready-flag race appears in D-225's Known gaps and in CONSOLIDATED, but not in SPEC §2.2, SPEC §4.1, GUIDE or README.
- **README's status line** says "It runs on … arm64 Linux, and cross-compiles between them" with no qualifier. No native arm64 run exists yet (D-225: the arm job's first results come at the next session).
- **How verified:** by reading the docs against CONSOLIDATED C356 and C357. qemu cannot show the race.
- **Fix:**
  - Say plainly in SPEC §4.1 and §2.2, GUIDE and README that on aarch64, until H35b, the race-safety invariant does not hold: a program that races can crash. Cover both cases: objects published before their fields (C356), and module-scope bindings or early cells seen ready before their values (C357).
  - Qualify README's status line until the arm64 job is green.

#### R1-F2 (low) The `PTS_SYSROOT` "default" in the help breaks the link if you set it, and the variable often has no effect
- **Where:** `cmd/pts/cli.go:59-60` ("default: where clang looks for the target's, /usr/<triple> on Debian") and `docs/SPEC.md:95`.
- **Repro:** `scripts/probe-sysroot.sh`, log in `logs/probe-sysroot.log`.
  ```
  $ PTS_SYSROOT=/usr/aarch64-linux-gnu pts build hello.pts --target aarch64-linux -o hs
  exit=1
  /usr/bin/aarch64-linux-gnu-ld: cannot find /usr/aarch64-linux-gnu/lib/libm.so.6 inside /usr/aarch64-linux-gnu
  clang: error: linker command failed with exit code 1
  ```
  Debian's `libm.so` linker script names absolute paths, which ld then looks up inside the sysroot.
- **The opposite case:** a bogus value links fine. `clang --target=aarch64-linux-gnu --sysroot=/nonexistent s.c -lm` exits 0. With `-v`, clang picks `/usr/lib/gcc-cross/aarch64-linux-gnu/13` and that installation's `aarch64-linux-gnu/include`. So when Debian's cross GCC is installed, clang does not take headers or libc from the sysroot.
- **Tests:** nothing exercises `PTS_SYSROOT` end to end. `TestOtherTargetPassesItsTripleAndSysroot` only checks the flags.
- **Fix:**
  - Don't present `/usr/<triple>` as the default. Say "default: none; clang finds Debian's and Ubuntu's cross packages itself. Set it only to a target root filesystem, with `usr/include` and `usr/lib/<triple>`."
  - Note that an installed cross GCC takes precedence.

#### R1-F3 (low) A unit test depends on the caller's environment
- **Where:** `pts/toolchain/target_test.go:31-38`. The first assertion assumes `PTS_SYSROOT` is unset.
- **Repro:** `scripts/probe-unit-sysroot.sh`.
  ```
  $ PTS_SYSROOT=/opt/my-arm-root go test -count=1 -run '^TestOtherTargetPassesItsTripleAndSysroot$' ./pts/toolchain/
  --- FAIL: TestOtherTargetPassesItsTripleAndSysroot
      target_test.go:35: ... - "--sysroot=/opt/my-arm-root",
  ```
- **Fix:** call `t.Setenv("PTS_SYSROOT", "")` first.

#### R1-F4 (low) `--target` doesn't override an invalid `PTS_TARGET`, and precedence has no test
- **Where:** `pts/driver/driver.go:222-228` and `pts/toolchain/toolchain.go:86-89`.
- **Repro:** `scripts/probe-cli.sh`, log in `logs/probe-cli.log`.
  ```
  $ PTS_TARGET=bogus pts build hello.pts --target aarch64-linux -o h4
  exit=1
  pts: PTS_TARGET: unknown target "bogus": pts builds for x86_64-linux or aarch64-linux
  ```
  This contradicts D-225's "`--target` … overrides it".
- **With valid values, precedence works** (checked by hand: `PTS_TARGET=aarch64-linux … --target x86_64-linux` gives x86-64). But no test checks either direction: the flag over the variable, or `PTS_TARGET` alone producing `EM_AARCH64` through the CLI.
- **Fix:**
  - Don't parse `PTS_TARGET` when a target is given, for example by passing the target into `toolchain.New`.
  - Add a CLI test for both directions, checking `elf.Machine`.

#### R1-F5 (low) Some tests and comments claim more than they check
- **`TestCrossBuildsKeepTheirOwnArchives`** (`target_test.go:54-62`) rebuilds Runtime's flag composition (`append(tc.targetFlags(), vendoredFlags...)`) and hashes it itself. It would still pass if `Runtime` hashed different flags from those it compiles with, so it mirrors the implementation instead of observing it. Better: run `Runtime` for the host and the other target into a temporary `CacheDir` and check the archive directories differ.
- **`TestBuildForThisMachinesTargetIsAPlainBuild`** checks only the program's output, not that the build was plain. Rename it, or check the cache directory names.
- **`TestRuntimeBuildsAndRunsForAarch64`'s comment** (`cross_test.go:24-25`) says the program runs "through the pool's workers, the collector and libuv's loop". `pts_parallel_threads()` starts the workers but they stay idle (`pool.c:310-313`), and `pts_runtime_exit` doesn't run the loop (`process.c:25-29`).
- **`runAarch64`** in `cmd/pts/cross_target_test.go:155-165` uses `cmd.Output()`. On failure the assertion shows only "exit status N", not stderr, and there is no per-program deadline: a hang under qemu waits for the 40-minute suite timeout.

#### R1-F6 (low, unverified) The `spin_pause` comment overstates what YIELD does
- **Where:** `pts/runtime/src/sync.c:35`, which says YIELD "gives the lock's holder its share".
- **Why I doubt it:** on cores without SMT (Neoverse, Ampere, Cobalt, Graviton), YIELD is effectively a NOP. Other projects use `isb` for spin waits for that reason (PostgreSQL's arm64 `spin_delay`; HotSpot's `OnSpinWaitInst=isb`).
- **Status:** this is from Arm documentation and other projects' practice. I couldn't measure it without arm hardware. It doesn't affect correctness, because the loop futex-waits after 100 spins.
- **Fix:** reword the comment, or use `isb sy` and measure on the arm job.

#### R1-F7 (nit)
- `pts/toolchain/libraries.go:77-78` still says libbacktrace's configure output is "for x86-64 Linux". ARCHITECTURE now says it holds for both.
- `toolchain.go:128-131` appends the cross-package hint to every `Runtime` error. That includes the "hashing … are the submodules checked out?" error and context cancellation.
- `process.c` stops with `#error` on other processors, while `spin_pause` silently does nothing there. Pick one behaviour.

#### R1-F8 (pre-existing on the base, outside H35a's scope) Deeply nested array to string crashes with SIGSEGV instead of RangeError
- **Repro:** `stack/j3.pts` and `scripts/probe-join.sh`: build a 2,000,000-deep nested array, then `String(top)`.
  - It crashes on the base x86 CLI (exit 139).
  - It crashes on the new x86 CLI and on aarch64 under qemu (exit 139).
  - It crashes the same way for `.join(",")`, `.toString()` and a template literal.
  - Node prints `RangeError Maximum call stack size exceeded`.
- **Cause:** gdb shows about 3,000 `pts_array_join` frames, then a fault in `GC_clear_stack_inner`. `pts_array_join` (`array.c:789`) has no `pts_stack_check`; only `flatten_into` (`array.c:1229`) and the JSON paths do. JSON.parse and JSON.stringify at the same depth throw RangeError correctly on both targets.
- **Fix:** record it as a new C-number (it breaks the "never crash" rule on both processors) and give it an H task. A `pts_stack_check()` in `pts_array_join` is the likely fix.

### Checked and fine
- **CLI parsing:**
  - `--target v`, `--target=v` and `-o`/`--output` in any order or form (`-o=`, `--output=` empty).
  - A missing or empty target gives "needs a value" with exit 2; an unknown one lists the targets with exit 2 and writes no output file.
  - `emit-c --target` is an unknown option (exit 2).
  - `run --target` and `test --target` are refused as before. A bad `PTS_TARGET` gives exit 1, not an internal compiler error.
- **Host builds unchanged:**
  - The base CLI and the new CLI give identical vendored archive keys (`gc-f24796ebc23bec1f`, `uv-2709…`, `ryu-f8a7…`, `pcre2-19a8…`, `mbedtls-4514…`, `backtrace-a62b…`). Only `ptsrt` changes, because its sources changed.
  - x86 `sync.c` object code is byte-identical.
  - The aarch64 archives get their own keys.
- **The reverse direction, mechanically:** I ran an arm64 build of the CLI under qemu as a stand-in arm64 host. `--target x86_64-linux` passed `--target=x86_64-linux-gnu`, built archives under separate keys and produced a working x86-64 program.
- **Stack guard** (`stack/deep.pts`), x86 and aarch64, `PTS_THREADS` 1 and 4, identical output:
  - main thread three times in a row, pool workers, another loop's task, mutual method recursion, a closure, an async function, a generator body and large frames.
  - Disassembly: `mov x9, sp; cmp; b.cc` after the prologue, TLS relaxed to local-exec.
  - Only three kinds of thread run JS: main, pool workers (`pool.c:269`) and loop workers (`scheduler.c:337`). All call `pts_stack_limit_init`. Generators and async functions are state machines with no stacks of their own (no `makecontext`).
- **Other cross results:**
  - `process.arch`: `linux arm64` and `linux x64`.
  - The cross test's program gives exactly the expected output under qemu.
  - `Error.stack` output is identical on both targets.
  - Compiler warnings on the runtime are the same on both targets (21 each, with `-Wtype-limits -Wsign-compare`).
- **Missing cross toolchain:** simulated with `--gcc-toolchain=/nonexistent`; the package hint is shown.
- **`TestProgramDependsOnlyOnLibc`'s ELF reading:** checked with readelf on both builds.
- **The parent's suite:** exit 0, 0 failures. All the new tests passed; none were skipped.
- **CI:**
  - Both workflow files parse as YAML.
  - The arm image has PowerShell 7.4.6 and clang 18.1.3 (image readme), and `go-install.ps1` handles arm64.
  - The repo is public (GitHub API: `private: false`), so the arm runner is free.
  - Caches are keyed by `runner.arch`.
  - `qemu … --version | head -1` passes under `-eo pipefail` (50 runs).
  - Stress time: the parent's `TestStress` leaf tests took 74.5 s summed, so ten rounds fit easily in 60 minutes and 100 rounds in the nightly's 120.

### Couldn't check
- Anything on real arm64 hardware: the arm job's native suite, sanitizers and TSan on aarch64 (Ubuntu's VMA/ASLR settings), the stress runs, whether YIELD helps, and the H35b races, which may also make the arm job flaky until H35b lands.
- Cross-compiling for x86_64 from a real arm64 host with Debian's packages (`gcc-x86-64-linux-gnu`, `libc6-dev-amd64-cross`). Only the stand-in host above.
- `PTS_SYSROOT` with a real target root filesystem.
- D-225 and SPEC §9.150 already describe the result of "the parent's [cross run] on the final code". That run's numbers aren't in `h35.md` yet; add them before committing.

---

### Parent's resolution of round 1
- **R1-F1:** fixed. SPEC §2.2 and §4.1, GUIDE, README (the status line qualified: "runs on x86-64 Linux; also builds for arm64 Linux … so far tested under qemu; on arm64, programs whose threads race on shared objects can still crash") and ARCHITECTURE rule 6 now say that on aarch64, until H35b, the race-safety invariant doesn't hold and a racing program can crash, through C356 and C357.
- **R1-F2:** reproduced (`--sysroot=/usr/aarch64-linux-gnu`: `cannot find /usr/aarch64-linux-gnu/lib/libmvec.so.1 inside /usr/aarch64-linux-gnu`; `--sysroot=/nonexistent`: links). `PTS_SYSROOT` removed: untestable here, and wrong as documented; another layout takes a `PTS_CC` wrapper with clang's `--sysroot` or `--gcc-toolchain` (D-225).
- **R1-F3:** gone with `PTS_SYSROOT`; the test is `TestOtherTargetPassesItsTriple`.
- **R1-F4:** fixed: `toolchain.NewFor(target)` doesn't read `PTS_TARGET` when given a target; `TestTargetOptionWinsOverPTSTarget` (a valid and an invalid `PTS_TARGET`) and `TestPTSTargetIsTheTargetOfABuildWithoutOne` (`EM_AARCH64`). RED on round 0's code: `{1  pts: PTS_TARGET: unknown target "bogus": …} != {0  }` (`red-r1f4.log`).
- **R1-F5:** `TestCrossBuildsKeepTheirOwnArchives` replaced by `TestCrossBuildsCompileForTheirTargetIntoArchivesOfTheirOwn` (`Runtime`, `CompileProgram` and `CheckProgram` through a recording compiler and archiver; mutants without the flags in `Runtime`'s library builds, or in `CheckProgram`, fail it); `TestBuildForThisMachinesTargetRunsHere` renamed; `TestRuntimeBuildsAndRunsForAarch64`'s comment says what the program does; `runAarch64` has a 2-minute deadline and shows stderr on failure.
- **R1-F6:** the comment reworded (YIELD does about nothing on cores without SMT, and the lock sleeps on its futex after `SPINS` tries either way); `yield` kept, as Linux's `cpu_relax`, until the arm job can measure `isb`.
- **R1-F7:** `libraries.go`'s comment fixed; the package hint only on the C compiler's failure for another target; `spin_pause` is `#error` on other processors, as `pts_process_arch`.
- **R1-F8:** recorded as C359 (pre-existing on both processors), → H29.
- The cross run's numbers: in [h35.md](h35.md).

## Round 2 (2026-10-26): READY

Reviewed: `/tmp/h35s/wt-y`, round 0's change plus round 1's fixes (24 files). Probes: [h35a-review2-probes.tar.gz](h35a-review2-probes.tar.gz) (79 files: scripts, logs, the round-0→1 diff, the mutant diffs; no binaries, no keys).

---

VERDICT: READY

The round-1 fixes hold up, and the earlier probes I re-ran against the new code all still pass. I found one low-severity problem (R2-F1): a cross build through a `PTS_CC` wrapper, the route the new docs recommend for other layouts, reuses plain clang's cached archives. The other two findings are doc wording. A sentence of docs fixes R2-F1, so none of them block. I built the round-1 CLI into `/tmp/h35s/review2/` and ran every probe pinned to `taskset -c 6,7`; I ran no suite. I didn't edit `wt-y`: `git status` shows the same 24 entries as at the start. The scripts, logs, the round-0→1 diff and the mutant diffs are in `/tmp/h35s/review2/h35a-review2-probes.tar.gz` (79 files, no binaries, no keys). None of my processes are left running.

### Findings

#### R2-F1 (low) A `PTS_CC` wrapper shares archive cache keys with plain clang
- **Where:** `docs/DECISIONS.md:2290` (D-225, Cross flags), `docs/SPEC.md:59`, `docs/ARCHITECTURE.md` §2.6 and `pts/toolchain/target.go:79` now say "another layout takes a `PTS_CC` wrapper that adds clang's `--sysroot` or `--gcc-toolchain`".
- **Why it's a problem:** the cache key hashes the compiler's `--version` output and the flags pts passes. It doesn't hash the wrapper or what the wrapper adds. D-225's own rejected alternative says the same ("the cache key hashes the compiler's `--version`, not the wrapper"). So a wrapper build for another sysroot reuses archives that plain clang built against Debian's cross headers, and the reverse.
- **Repro** (`scripts/probe-wrapper-cache.sh`): with the cache already holding plain clang's aarch64 archives, `PTS_CC=…/clang-other-layout` (`exec clang --gcc-toolchain=/nonexistent --sysroot=/nonexistent "$@"`) runs `pts build hello.pts --target aarch64-linux`:
  ```
  wrapper build exit=1
  pts: …/clang-other-layout --target=aarch64-linux-gnu -O2 … -o …/hw …   (the main.c compile, not "building ptsrt")
  fatal error: 'setjmp.h' file not found
  no new archives: the wrapper's build took plain clang's cached aarch64 archives
  ```
  With an empty cache, the same wrapper fails at `building ptsrt: …`, so the first run never rebuilt the archives for the wrapper.
- **Fix:** have the docs say to give each wrapper its own `PTS_CACHE_DIR`. Or hash `t.CC` into the key when `PTS_CC` is set: the default `clang` stays out of the key, so the host's keys don't change.

#### R2-F2 (low, doc wording) D-225's text doesn't fully match the fixes
- **Known gaps** (D-225, `docs/DECISIONS.md` at the entry's Known gaps line) still says only "may, on arm, see it before its fields … a ready flag before its value". SPEC, GUIDE, README and ARCHITECTURE now say the invariant doesn't hold and a racing program can crash; Known gaps should say the same.
- **"the parent's on the final code"** (D-225's cross-compiled-suite bullet): the qemu run (3,800 pass / 401 skip / 42 leaf failures) was on round 0's code. The round-1 changes don't change what gets built for aarch64 in that run: `sync.c` gained only a comment and an `#error`, and `PTS_SYSROOT` was unset. Still, "round 0's code" would be accurate.
- **The failure description** in that bullet says the programs "pass the tests' 30-second deadline". `TestRun/timers` didn't hit the deadline: it printed its ticks in another order under load (1.12 s) and passes alone (0.67 s in `alone.jsonl`). `h35.md:54` already describes it correctly.

#### R2-F3 (nit) A sentence in GUIDE will go stale
`docs/GUIDE.md:146` says "The arm64 CI job runs the whole suite natively; its first results come after this change." That is user-facing and will read wrongly once the job has run. It belongs in D-225 or PROGRESS.

### Round-1 fixes, verified
- **R1-F1:** fixed.
  - SPEC §2.2 and §4.1 now say that on aarch64 "this invariant doesn't hold" and a racing program can crash, through both C356 and C357. GUIDE, README (status line and Known gaps) and ARCHITECTURE rule 6 say the same.
  - README's status line is qualified ("so far tested under qemu; … can still crash").
- **R1-F2 and R1-F3:** fixed. `PTS_SYSROOT` is gone from code, help, SPEC and tests; `grep -rn PTS_SYSROOT` finds only D-225's history note.
- **R1-F4:** fixed. `scripts/probe-cli.sh` (log `logs/probe-cli.log`):
  - `PTS_TARGET=bogus pts build hello.pts --target aarch64-linux` now exits 0.
  - `PTS_TARGET=aarch64-linux … --target x86_64-linux` gives an x86-64 ELF.
  - A bad `PTS_TARGET` with no flag still exits 1.
  - Mutant m5 (the env wins whenever it is set) fails `TestTargetOptionWinsOverPTSTarget`; the control run passes both new tests. The tests use `t.Setenv` without `t.Parallel`, which is safe because Go holds parallel top-level tests back until the sequential ones finish.
- **R1-F5:** `TestCrossBuildsCompileForTheirTargetIntoArchivesOfTheirOwn` observes the real code paths instead of copying them. Each of my four mutants, run through `go test -overlay`, fails it with `--- FAIL` (none was a build failure):
  - m1: `CheckProgram` without the target flags fails at the `--target=…` prefix.
  - m2: a cache key that drops `--target=` fails with `231 != 2`, because the cross build reused the host's archives.
  - m3: the link without the target flags fails.
  - m4: library builds without the target flags fail.
  - The control passes with `-count=3`.
  - The recording script appends lines well under dash's 8 KiB buffer, so the parallel compiles can't interleave a line.
  - The other three changes are fine: the rename, the corrected comment, and `runAarch64`'s 2-minute deadline that now shows stderr on failure.
- **R1-F6:** the comment is now accurate. Keeping `yield` matches Linux's arm64 `cpu_relax`.
- **R1-F7:** the package hint now appears only for a failing compiler on a cross build. `scripts/probe-hint.sh` with a failing compiler: no hint for the host build, a hint with `--target aarch64-linux`. An interrupted cross build (SIGINT after 4 s) exits 130 with empty stderr. `spin_pause` now stops with `#error` on other processors. The `libraries.go` comment is fixed.
- **R1-F8:** recorded as C359 in CONSOLIDATED and listed under H29.

### Re-run and still fine
- **CLI parsing and exit codes:** every round-1 case gives the same result.
- **Stack guard** (`scripts/probe-stack.sh`): x86 and aarch64 give identical output at `PTS_THREADS` 1 and 4, and the disassembly still shows `mov x9, sp; cmp; b.cc` after the prologue.
- **The qemu run's evidence** (`/tmp/h35s/h35a/cross/`):
  - `classify.py` agrees: 3,800 pass, 401 skip, 42 leaf failures (32 sanitizer link failures, 10 others).
  - Every one of the 10 others passes in `alone.jsonl`.
  - `timers_refused_no_leak`'s direct runs (`got-2.txt`, `got-4.txt`) are byte-identical to `want.txt`.
- **The parent's round-1 suite** (still running when I checked): 0 failures so far, and all 17 new or changed tests pass.

### Couldn't check
The same as round 1: anything on real arm64 hardware (the arm job, sanitizers and TSan on aarch64, the stress runs, the H35b races) and the x86-64 cross build from a real arm64 host.

---

### Parent's resolution of round 2
- **R2-F1:** reproduced in the reviewer's probe log; documented rather than keyed: D-225, SPEC §2.2 and ARCHITECTURE §2.6 now say a wrapper needs a `PTS_CACHE_DIR` of its own, since the archives' keys hash the compiler's `--version` and pts's flags, not what a wrapper adds. Keying on `PTS_CC`'s path would still miss a wrapper edited in place.
- **R2-F2:** D-225's Known gaps now say the invariant doesn't hold on arm and a racing program can crash; the cross-run bullet says round 0's code and describes the timer test's reordering.
- **R2-F3:** the sentence is gone from GUIDE (the arm job's results go in PROGRESS when they come).
- These are documentation changes only, made after the round-1 gate suite; the snapshot's code and tests are byte-identical to what it ran.
