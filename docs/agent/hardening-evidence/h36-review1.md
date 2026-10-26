# H36 review round 1: the arm64 CI fix (D-227)

The `reviewer`'s report, verbatim.

**Verdict: READY.** I found no high or medium problems. There are seven low findings: comment and documentation accuracy, two IDs that point nowhere yet, and the TSV's provenance notes. None changes behaviour. Nothing in the main tree was edited; the eight reviewed files' sha256 matched at the end (`/tmp/pts-h36/review1/snap-files.sha256`).

## Verified by running

All in my own worktrees: `/tmp/pts-h36/review1/snap` is HEAD plus the change, `/tmp/pts-h36/review1/headwt` is HEAD. Cache was `/tmp/pts-h36/review1/cache`, one heavy job at a time.

**1. The merged ieee754.c**
- Preprocessed output (`clang -E -P`, whitespace normalised): new equals HEAD on x86_64-linux-gnu, and equals the reproducer's prototype on aarch64-linux-gnu (`pp/pp_check.sh`).
- Removing the fused branches and the include line gives HEAD's file byte for byte, so every `#else` branch is HEAD's code and comments.
- There are 52 `#if PTS_V8_GCC_FMA` blocks, every one balanced, and no `PTS_V8_FMA` appears outside a fused branch.
- Both targets compile with `-Wall -Wextra -Wshadow` and no warnings.
- x86-64 object code is identical to HEAD for `ieee754.o` and `date.o`. `number_methods.o` equals HEAD plus only the zero-skipping loop.
- aarch64 now has 277 fmadd/fmsub-family instructions in ieee754.o (0 at HEAD).
- The macro is 0 for arm64-apple-macos and x86_64-apple-macos, so macOS is untouched.

**2. Independent Math spot check** (my own C harness, seed and input mixes; harness built with the runtime's release flags)
- 40,000 inputs per function, 20 functions: 0 mismatches against x86 Node on x86, and 0 against arm64 Node under qemu. HEAD's aarch64 build had 1,962 mismatches on the same inputs.
- 2.3M further inputs aimed at the rare cases (near √2 for log, log10 and log2; cbrt; log1p): 0 mismatches on both architectures, against 8,083 at HEAD.
- The harness can tell fused from unfused: 6 of 7 single-site mutants failed against arm64 Node (25 to 282 mismatches each; the log mutant only on the larger inputs). The one that passed, `t - fn * pio2_1`, has an exact product, so fusing it cannot change anything.

**3. parseInt leading zeros**
- 1,500 random strings in every radix that isn't a power of two, with leading zeros, signs, Unicode spaces, trailing junk and mixed case.
- Plus edge cases in every such radix and 36: `"0"`, `"-000"` with `Object.is(..., -0)`, `"0x1z"`, `"00x1z"`, `"0."`, `"  -00"`, and `0x` prefixes in radix 16 and with no radix.
- Results (4,208 lines in total, including the Date probes): 0 differences on x86 against x86 Node and 0 on aarch64 against arm64 Node. HEAD's runtime differs on 274 and 657 lines.
- The fix matches V8, which skips leading zeros for every radix before grouping digits. It only reaches `other_radix_digits`; radix 10 and the power-of-two radixes already handled zeros.

**4. MakeDate and MakeTime**
- MakeDate probes through every caller: `Date.UTC`, `new Date(y, m, d, ...)` in local time, and `setUTCDate`, `setUTCHours`, `setUTCMinutes`, `setHours`, `setUTCFullYear`. The two Nodes differ on 345 of these lines; PTS matches both architectures on all of them.
- MakeTime cases with large hours/minutes/seconds that cancel each other: I checked exactly that a fused MakeTime would change all 150 of the two-field cases. Both Nodes still agree on all 1,350 MakeTime cases. So Node's arm64 binary does not fuse MakeTime, and leaving it unfused is right.
- `toString(radix)`, `toFixed`, `toPrecision`, `**` and `%`: 0 differences between the two Nodes over 150,000 lines, so nothing else needs porting.

**5. RED and GREEN**
- `TestNumbersPrintAsNodeOfTheProgramsArchitecture` passes on x86 and on `PTS_TARGET=aarch64-linux` (exit 0). With HEAD's runtime (`PTS_HOME=headwt`) it fails with 8 and 377 differing lines (exit 1).
- `TestBuildForAarch64LinuxPrintsNumbersAsNodeOnArm64` passes now, and fails with 377 differing lines on HEAD.
- The tests that failed in CI, run under qemu against arm64 Node (`methods_1`, `methods_2`, `math`, `number_library`, `math_library`, plus `date_library`): all pass now. On HEAD exactly CI's five fail and `date_library` passes, so qemu reproduces the CI failures.
- qemu arm64 Node prints the real runner's values for both CI diffs (`23.739560053478666` and `3.9284065583537005e+29`).
- On x86, the number corpus (9 tests) and the number/math/date/parse differential tests (6) pass. `TestPublish*`, `TestRuntimeUnitTests` (x86) and gofmt pass, and go vet ran on the three packages.

**6. TSV provenance**
- Both columns match what I re-recorded from x86 Node 24.21.0 and from arm64 Node under qemu with `TZ=America/New_York` (381 rows each).
- The arm64 tarball's sha256 `6ad1325e…9ad2` matches nodejs.org's SHASUMS256.txt.
- The binary's compiler string is `GCC: (GNU) 12.2.1 20221121 (Red Hat 12.2.1-7)`.

**7. How much the TSV actually pins**
- I made one mutant per fused block, putting V8's unfused form back. 20 of the 52 make at least 3 TSV rows fail.
- 31 change nothing on the TSV or on my 2.9M inputs. Their products are exact (`fn*pio2_1`/`pio2_2`, `two24*fw`, `8*floor`, the 24-bit chunk products in `__kernel_rem_pio2`, `0.5*`, `2*`), or their effect is far below an ulp.
- One mutant (cbrt's Newton block) doesn't compile on its own; see R1-F5.
- The TSV catches acosh, asinh and asin(>0.975), which my random inputs missed.

## Findings

**R1-F1 (low): the header's rule for when GCC fuses is wrong.**
`pts/runtime/src/v8_fma_internal.h:4-5` says GCC fuses "a product into the addition or subtraction that is its only use". GCC fuses when *every* use of the product is an addition or subtraction in the same basic block, copying the product into each one. ieee754.c's own comments depend on that ("fused into both of its uses" at lines 258 and 272; "fused into each of its uses" in log2).
- Fix: "…fuses a product into each addition or subtraction that uses it, when every use is one and all are in its basic block".

**R1-F2 (low): the TSV header leaves out the time zone and how to regenerate it, and overstates what it holds.**
- Three `new Date(2000, 0, D, h)` rows change by 4 h under `TZ=UTC` (checked: `tsv/x64_utc.txt`).
- Line 1, and the comments on `nodeArchNumbersFile` (`node_arch_test.go:12`) and `nodeArchNumbers` (`cross_target_test.go:62`), say every expression prints differently on the two architectures. 12 rows print the same (the 4 pinned Math rows and the 8 leading-zero rows).
- Fix: add a header line such as `# recorded with TZ=America/New_York: grep -v '^#' F | cut -f1 | sed 's/.*/console.log(&);/' > p.mjs; TZ=America/New_York node p.mjs`, with the arm64 tarball's sha256. Reword "Expressions whose results … print differently" to "Expressions whose results depend on Node's architecture, and a few that pin related behaviour".

**R1-F3 (low): references that point nowhere yet.**
- `(C362)` in `v8_fma_internal.h:2` is not defined anywhere in the repo.
- `(H36)` in `node_arch_test.go:12` names the macOS task in `HARDENING_PLAN.md:107`, not this CI fix.
- Fix: in the commit, add C362 and a D-entry covering why `#if` blocks instead of the always-macro form, and the known gaps below. Record the reproducer's unfixed `make_day` ordering finding as its own C-number. Reference the actual task ID. My two probes of that finding (`probes/pre.pts`) printed the same as Node, so I neither confirmed nor refuted it.

**R1-F4 (low, ahead of H36): two call sites will need a third mode on macOS.**
When `PTS_V8_GCC_FMA` is 0, `PTS_V8_FMA` is never fused. `date.c:110` and `number_methods.c:461` are single-expression `a*b+c`, which Apple clang's default `-ffp-contract=on` fuses in Node's macOS build. If H36 adds `#pragma STDC FP_CONTRACT ON` to ieee754.c only, these two will stay unfused on macOS. `TestNumbersPrintAsNodeOfTheProgramsArchitecture` will also `Fatalf` on darwin-arm64 until the TSV has a darwin column.
- Fix for H36: a mode such as "fused by any contracting compiler" for these two sites, and a darwin-arm64 column.

**R1-F5 (low, nit): some blocks are fragile to edit.**
- Several blocks open a `{` inside the `#if` that closes after `#endif`: `__kernel_cos` 373-381, `__kernel_sin` 778-786, acos 1015-1041, atan 1379-1395, expm1 2673-2683.
- cbrt's declaration block (2749, which drops `w`) is tied to its Newton block (2819). Unfusing only the Newton block fails to build (mutant m49).
- Both architectures compile in CI, so a mismatch would be caught there.
- Fix (optional): a one-line comment at 2749 that `w` is only used by the `#else` form at 2819.

**R1-F6 (low): `assembly()` drops clang's stderr.**
In `pts/toolchain/publish_test.go` (around line 66), `assembly()` uses `.Output()`, so CI showed only "exit status 1". The missing-x86-headers cause was inferred, not seen. The new skip is sound: it returns at once when GOARCH is amd64, so it hides nothing on x86. I checked that its `#include <stdlib.h>` probe fails without headers, and its skip message includes clang's error.
- Fix (optional, outside the diff): include stderr in `assembly`'s error message.

**R1-F7 (info): arm64 results are now tied to Node's build compiler.**
They match Node's linux-arm64 build made with GCC 12.2.1 (Red Hat gcc-toolset-12). If a later Node 24.x arm64 build uses another GCC or clang, the arm64 differential tests will fail again.
- Fix: record it as a known gap in the D-entry, with the check `strings -a node | grep 'GCC: ('`.

## Inferred, not verified
- The CI arm64 runner lacks x86-64 libc headers. The log only shows "exit status 1"; the next CI run's skip message will show clang's error.
- The native arm64 runner will behave as qemu did. qemu's FMA is correctly rounded, and the two CI values matched.

## Probes
- `/tmp/pts-h36/review1/review1-probes.tar.gz` (65 entries, checked with `tar tzf`, no private keys). It holds the scripts, inputs (regenerable from seeds), outputs, logs, and the mutant tables `blockmut/mutants.txt` and `mut/results.txt`.
- The `logs/qemu_*.txt` files hold only the HEAD runs (my file-naming slip); the passing results are quoted above.
- I created two worktrees, `/tmp/pts-h36/review1/snap` and `/tmp/pts-h36/review1/headwt`; remove them with `git worktree remove --force <path>` when done.