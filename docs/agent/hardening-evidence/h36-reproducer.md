# H36 reproducer report: why aarch64 Math, parseInt and Date output differs from arm64 Node, the fix, and the regression tests

**Result:** GCC 12's choice of which products to fuse explains every difference between PTS-aarch64 and arm64 Node. A C port with an explicit `PTS_V8_FMA(a, b, c)` at exactly GCC 12's fused sites matches arm64 Node on 98.4M Math inputs (0 mismatches). It still matches x86 Node on x86-64 (0 mismatches), and x86 Math output is unchanged from today on all 96M of the large set. The prototype also turns the parent's focused RED command green, while the file keeps `-ffp-contract=off`.

Nothing is committed. My worktree's `pts/third_party/*` are symlinks to your submodules, so `git status` shows them as typechanges: don't commit those.

## 1. Mismatch counts per function and variant (run, not inferred)

Inputs: 120,000 per function, 2.4M in total. Each function gets 30k uniform 64-bit patterns, 20k in the corpus style, and 70k dense around its branch thresholds; the CI and residual cases are included. Generator: `gen_inputs.py`.

aarch64 variants of HEAD's port, each against arm64 Node 24.21.0 (official binary under qemu):

| fn | (a) clang off (today) | (b) `#pragma clang fp contract(fast)` | (b2) clang `-ffp-contract=fast` | (c) clang `on` | (c2) `STDC FP_CONTRACT ON` | (d) gcc13 -O2 fast | (d) gcc13 -O3 fast | gcc12.3 -O3 fast | **prototype** |
|---|---|---|---|---|---|---|---|---|---|
| acos | 179 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| acosh | 35 | 96 | 96 | 106 | 106 | 0 | 0 | 0 | 0 |
| asin | 384 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| asinh | 52 | 168 | 168 | 168 | 168 | 0 | 0 | 0 | 0 |
| atan | 75 | 186 | 186 | 186 | 186 | 0 | 0 | 0 | 0 |
| atan2 | 47 | 103 | 103 | 103 | 103 | 0 | 0 | 0 | 0 |
| atanh | 20 | 72 | 72 | 72 | 72 | 0 | 0 | 0 | 0 |
| cbrt | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| cos | 282 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| cosh | 28 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| exp | 25 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| expm1 | 54 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| log | 0 | 553 | 553 | 574 | 574 | 0 | 0 | 0 | 0 |
| log10 | 0 | 547 | 547 | 586 | 586 | 0 | 0 | 0 | 0 |
| log1p | 62 | 290 | 290 | 290 | 290 | 0 | 0 | 0 | 0 |
| log2 | 571 | 598 | 598 | 571 | 571 | 0 | 0 | 0 | 0 |
| sin | 505 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| sinh | 59 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| tan | 711 | 0 | 0 | 25 | 25 | 0 | 0 | 0 | 0 |
| tanh | 19 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| **total** | **3108** | **2613** | 2613 | 2681 | 2681 | **0** | 0 | 0 | **0** |

- (a) on aarch64 equals x86 Node exactly: x86 Node also differs from arm64 Node in 3108 places.
- On x86-64, (a) against x86 Node is 0 in every function.
- The clang `fast` pragma is worse than it looked in the corpus. It makes `log` and `log10` wrong where today's code is right.
- Larger run on 4.8M inputs per function (96M; `out/big_cmp_*.txt`):
  - prototype-aarch64 against arm64 Node: 0.
  - the all-fused result computed on x86 with FMA hardware, against arm64 Node: 0.
  - prototype-x86 against x86 Node: 0, and HEAD-x86 against x86 Node: 0.
  - The two Nodes differ on 124,743 of the 96M.

## 2. Where GCC fuses, and where clang decides differently

- **Ground truth from Node's binary.** It exports `v8::base::ieee754::*` and keeps its local symbols. Its compiler string is `GCC: (GNU) 12.2.1 20221121 (Red Hat 12.2.1-7)`. I counted the fused instructions in each function (`disasm/node_counts.txt`).
- **GCC 12.3 matches it.** I fetched GCC 12.3 without root (`get_gcc12.sh`, Ubuntu's signed packages). Built with `-O3 -ffp-contract=fast`, every function has Node's per-function counts. The exceptions are sin and cos, where only how much code is inlined or duplicated differs: the fused counts match after subtracting the inlined `rem_pio2`, and cos has one extra copy of a block in Node.
- **Site list.** `-fdump-tree-widening_mul-lineno` gives 198 fused sites on 103 source lines (`gimple12/sites_gcc12_O3.txt`). GCC 12 and 13 agree on the set at both -O2 and -O3, except at one site:
  - `asinh`, `w = log(2.0*t + one/(sqrt(x*x + one) + t))`: GCC 12 fuses `x*x + one`, and Node shows the same `fmadd d0,d1,d1,d0` with d0 = 1.0. GCC 13 instead fuses the exact `2.0*t`.
  - Arm64 Node confirms GCC 12's choice: `asinh(-2.666121436240375)`, `asinh(-2.709635790005804)` and `asinh(-2.8031093999042684)` match only GCC 12's choice. These cases are in the golden file.
  - This is the risk in using GCC 13.3 instead of 12.2: it is wrong at 3 of 4.8M asinh inputs.
- **Patterns in GCC 12's choices.**
  - It fuses across statements:
    - a product whose only use is an add or subtract in the same basic block;
    - each use of a product used twice (`w = fn*pio2_2` in `rem_pio2`, `val_hi` and `hfsq` in log2);
    - `k_log1p`'s `s*(hfsq+R)` into log2's `lo` sum.
  - It does not fuse where PRE hoists a product shared by two branches, which gives it two uses: atan's `x*(s1+s2)`, log1p's and log's `s*(hfsq+R)` and `s*(f-R)`.
  - It does not fuse where the math-errno `sqrt` check splits the basic block: acosh's `2.0*x`.
- **Why clang `fast` leaves the 4 residual lines** (from `objdump -dl` of b2):
  - **atan:** clang rounds `s1 = z*poly` and fuses `x - x*(s1+s2)` (fmsub) and `x*(s1+s2) - atanlo` (fnmsub). GCC does the reverse: it fuses `z*poly` into `s1+s2` and leaves `x*(...)` rounded. This explains both atan residuals.
  - **log1p**, which atanh and asinh reach: clang rounds `R = z*poly` and fuses `hfsq - s*(hfsq+R)`. GCC fuses `z*poly` into `hfsq+R` and leaves `s*(...)` rounded. This explains the atanh and asinh residuals.
  - **log:** clang additionally fuses the k==0 lines 1735 and 1740, which explains its 553 log mismatches.
- **Each sensitive site checked against arm64 Node.** A site-sensitivity build (`sitetest/`) numbers every `PTS_V8_FMA` (200 of them) and turns them off one at a time over the 96M inputs.
  - 81 sites change at least one result on their own.
  - For all 1826 inputs found that way, arm64 Node equals the all-fused prototype, and never the site-unfused value. So each of those 81 sites is confirmed fused in Node.
  - 119 sites never change a result on their own, in 96M inputs. Some are exact products (2.0, 0.5, `two24`, 24-bit chunk products); the others have too little influence. For those, the evidence is GCC 12's site list plus the matching instruction counts.

## 3. Other runtime numeric code

Each was probed with programs that run under both Nodes and under pts (`probes/*.pts`; field-level diffs from `fielddiff.py`):

| area | x86 Node vs arm64 Node | PTS-aarch64 HEAD vs arm64 Node | prototype vs arm64 Node |
|---|---|---|---|
| parseInt, radixes 2–36, long strings (3001) | 271 lines differ | 375 (220 fusion + 155 leading-zero bug) | 140 → **0** with the leading-zero fix |
| `toString(radix)`, then parseInt/parseFloat of it (2000) | 66, only the parseInt field | 66 | 0 |
| `toFixed`, `toPrecision`, `toExponential` (also at 100 digits), `toLocaleString`, `String` (2500) | 0 | 0 | 0 |
| `parseFloat`, `Number(s)`, `JSON.parse`, `JSON.stringify` (2000) | 0 | 0 | 0 |
| `hypot`, `**`, `pow`, `sqrt`, `fround`, user `x*y+z` (2000) | 0 | 0 | 0 |
| Date: `Date.UTC` / `new Date(...)` with a huge day count cancelled by a time (1500), plus 500 ordinary dates, setters and parse | 1202 | 1202 | **0** |

- **The whole-binary scan** (`disasm/node_fused_functions.txt`, 176 functions) finds only these fused V8 sites that PTS ports or calls:
  - `NumberParseIntHelper::HandleGenericCase` (`result*multiplier + part`), fixed in the prototype.
  - `MakeDate` (`day*kMsPerDay + time`), fixed in the prototype.
  - `ParseDateTimeString` (inlined MakeDate; exact for parsed values).
  - `BignumDtoa`'s `EstimatePower`. A brute force over every exponent shows the estimate never changes (`probes_extra/estimate_power.c`).
- `MakeTime` and `MakeDay` are not fused: their DoubleToInteger branches split the basic blocks.
- `DoubleToRadixStringView`, `Strtod`, `StringToDouble` and the fixed, precision and exponential conversions have no fused instructions.
- `Math.hypot` is Torque, and `**` calls the system glibc's `pow`.
- V8's JIT tiers never fuse user arithmetic. `probes_extra/turbofan_fma.mjs` prints 0 (unfused) in the interpreter, Maglev, TurboFan and a hot loop on arm64 Node. Generated code must keep `-ffp-contract=off`.

## 4. Commands and exit codes

All run from `/tmp/pts-h36/repro` or my worktree.

| command | exit | result |
|---|---|---|
| `build_variants.sh`, `run_variants.sh` | 0 / 0 | table above; arm64 Node took 5.4 s |
| `compare.py` on the 2.4M set | – | table above |
| `build_proto.sh`, `run_big.sh` (stopped after the harness stages), then `cmpbits` | 0 | the 96M results above |
| `site_harness inputs_big … site_funcs.txt` | 0 | 81 of 200 sites sensitive |
| `eval_vectors.mjs` under both Nodes | 0 | 1826 of 1826 inputs agree with all-fused |
| `run_probes_node.sh`, `run_probes_pts.sh` (both architectures, HEAD and prototype homes) | 0 | probe table above |
| `run_focused.sh … aarch64 home_head` (your RED command on HEAD) | **1** | `methods_1`, `methods_2` and `math` FAIL (9/6/6 lines); `TestRunMatchesNode/{math_library,number_library}` FAIL |
| `run_focused.sh … aarch64` (prototype) | **0** | all 8 corpus parts and both run tests PASS (`-v`) |
| `run_focused.sh … x86` (prototype) | 0 | all PASS |
| `run_new_tests.sh` | see §6 | RED on HEAD, GREEN on the prototype |
| `run_toolchain_tests.sh` | 0 / 0 | `TestRuntimeUnitTestsPassUnderSanitizers` (21 C tests), `TestRuntimeBuildsAndRunsForAarch64`, `TestNumberFormattingMatchesJavaScript`, and every `Aarch64` / `Target` test in cmd/pts PASS |
| `clang -Wall -Wextra -fsyntax-only` on the three changed files, x86 and aarch64 | 0 | no warnings |

## 5. Recommended fix (unified diff against HEAD)

The shape of the fix:
- A new `pts/runtime/src/v8_fma_internal.h` defines `PTS_V8_FMA` as `__builtin_fma` on `__aarch64__ && __linux__` and as `a*b+c` elsewhere.
- It is written at exactly GCC 12's sites, and the files stay at `-ffp-contract=off`. It doesn't depend on any clang heuristic: `__builtin_fma` is an exact `fmadd` on every clang version.

Leave out or keep separately: the three-line `while (i < n && s[i] == '0')` hunk in `number_methods.c` is a separate fix (§7.1). It is not about fusion. Only this hunk, and only for parseInt strings with leading zeros, changes x86 output (it now matches x86 Node).

The same diff is in `patches/runtime_U1.diff`, which `patch --dry-run` applies cleanly to HEAD.

```diff
diff --git a/pts/runtime/src/date.c b/pts/runtime/src/date.c
--- a/pts/runtime/src/date.c
+++ b/pts/runtime/src/date.c
@@ -8,2 +8,3 @@
 #include "pts.h"
+#include "v8_fma_internal.h"
 
@@ -108,3 +109,3 @@ static double make_date(double day, double time) {
   }
-  return day * MS_PER_DAY + time;
+  return PTS_V8_FMA(day, (double)MS_PER_DAY, time); /* fused as in V8's MakeDate on arm64 Linux */
 }
diff --git a/pts/runtime/src/ieee754.c b/pts/runtime/src/ieee754.c
--- a/pts/runtime/src/ieee754.c
+++ b/pts/runtime/src/ieee754.c
@@ -48,2 +48,3 @@
 #include "pts.h"
+#include "v8_fma_internal.h"
 
@@ -236,5 +237,5 @@ static int32_t __ieee754_rem_pio2(double x, double *y) {
     t = fabs(x);
-    n = (int32_t)(t * invpio2 + half);
+    n = (int32_t)PTS_V8_FMA(t, invpio2, half);
     fn = (double)(n);
-    r = t - fn * pio2_1;
+    r = PTS_V8_FMA(-fn, pio2_1, t); /* t - fn * pio2_1 */
     w = fn * pio2_1t; /* 1st round good to 85 bit */
@@ -249,6 +250,5 @@ static int32_t __ieee754_rem_pio2(double x, double *y) {
       if (i > 16) { /* 2nd iteration needed, good to 118 */
-        t = r;
-        w = fn * pio2_2;
-        r = t - w;
-        w = fn * pio2_2t - ((t - r) - w);
+        t = r; /* w = fn * pio2_2 is fused into both of its uses */
+        r = PTS_V8_FMA(-fn, pio2_2, t);
+        w = PTS_V8_FMA(fn, pio2_2t, -PTS_V8_FMA(-fn, pio2_2, t - r)); /* fn * pio2_2t - ((t - r) - w) */
         y[0] = r - w;
@@ -257,6 +257,5 @@ static int32_t __ieee754_rem_pio2(double x, double *y) {
         if (i > 49) { /* 3rd iteration need, 151 bits acc */
-          t = r;      /* will cover all possible cases */
-          w = fn * pio2_3;
-          r = t - w;
-          w = fn * pio2_3t - ((t - r) - w);
+          t = r; /* will cover all possible cases; w = fn * pio2_3 is fused into both of its uses */
+          r = PTS_V8_FMA(-fn, pio2_3, t);
+          w = PTS_V8_FMA(fn, pio2_3t, -PTS_V8_FMA(-fn, pio2_3, t - r)); /* fn * pio2_3t - ((t - r) - w) */
           y[0] = r - w;
@@ -353,5 +352,5 @@ static inline double __kernel_cos(double x, double y) {
   z = x * x;
-  r = z * (C1 + z * (C2 + z * (C3 + z * (C4 + z * (C5 + z * C6)))));
+  r = z * PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, C6, C5), C4), C3), C2), C1);
   if (ix < 0x3FD33333) { /* if |x| < 0.3 */
-    return one - (0.5 * z - (z * r - x * y));
+    return one - PTS_V8_FMA(0.5, z, -PTS_V8_FMA(z, r, -(x * y))); /* one - (0.5 * z - (z * r - x * y)) */
   } else {
@@ -362,5 +361,5 @@ static inline double __kernel_cos(double x, double y) {
     }
-    iz = 0.5 * z - qx;
+    iz = PTS_V8_FMA(0.5, z, -qx);
     a = one - qx;
-    return a - (iz - (z * r - x * y));
+    return a - (iz - PTS_V8_FMA(z, r, -(x * y)));
   }
@@ -522,3 +521,3 @@ static int __kernel_rem_pio2(double *x, double *y, int e0, int nx, int prec,
   for (i = 0; i <= jk; i++) {
-    for (j = 0, fw = 0.0; j <= jx; j++) fw += x[j] * f[jx + i - j];
+    for (j = 0, fw = 0.0; j <= jx; j++) fw = PTS_V8_FMA(x[j], f[jx + i - j], fw);
     q[i] = fw;
@@ -531,3 +530,3 @@ recompute:
     fw = (double)((int32_t)(twon24 * z));
-    iq[i] = (int32_t)(z - two24 * fw);
+    iq[i] = (int32_t)PTS_V8_FMA(-two24, fw, z);
     z = q[j - 1] + fw;
@@ -537,3 +536,3 @@ recompute:
   z = scalbn(z, q0);           /* actual value of z */
-  z -= 8.0 * floor(z * 0.125); /* trim off integer >= 8 */
+  z = PTS_V8_FMA(-8.0, floor(z * 0.125), z); /* trim off integer >= 8 */
   n = (int32_t)(z);
@@ -593,3 +592,3 @@ recompute:
         f[jx + i] = ipio2[jv + i];
-        for (j = 0, fw = 0.0; j <= jx; j++) fw += x[j] * f[jx + i - j];
+        for (j = 0, fw = 0.0; j <= jx; j++) fw = PTS_V8_FMA(x[j], f[jx + i - j], fw);
         q[i] = fw;
@@ -613,3 +612,3 @@ recompute:
       fw = (double)((int32_t)(twon24 * z));
-      iq[jz] = z - two24 * fw;
+      iq[jz] = PTS_V8_FMA(-two24, fw, z);
       jz += 1;
@@ -631,3 +630,3 @@ recompute:
   for (i = jz; i >= 0; i--) {
-    for (fw = 0.0, k = 0; k <= jp && k <= jz - i; k++) fw += PIo2[k] * q[i + k];
+    for (fw = 0.0, k = 0; k <= jp && k <= jz - i; k++) fw = PTS_V8_FMA(PIo2[k], q[i + k], fw);
     fq[jz - i] = fw;
@@ -722,7 +721,8 @@ static inline double __kernel_sin(double x, double y, int iy) {
   v = z * x;
-  r = S2 + z * (S3 + z * (S4 + z * (S5 + z * S6)));
+  r = PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, S6, S5), S4), S3), S2);
   if (iy == 0) {
-    return x + v * (S1 + z * r);
+    return PTS_V8_FMA(v, PTS_V8_FMA(z, r, S1), x);
   } else {
-    return x - ((z * (half * y - v * r) - y) - v * S1);
+    /* x - ((z * (half * y - v * r) - y) - v * S1) */
+    return x - PTS_V8_FMA(-v, S1, PTS_V8_FMA(z, PTS_V8_FMA(half, y, -(v * r)), -y));
   }
@@ -809,4 +809,4 @@ static double __kernel_tan(double x, double y, int iy) {
           SET_LOW_WORD(t, 0);
-          s = one + t * z;
-          return t + a * (s + t * v);
+          s = PTS_V8_FMA(t, z, one);
+          return PTS_V8_FMA(a, PTS_V8_FMA(t, v, s), t);
         }
@@ -832,8 +832,8 @@ static double __kernel_tan(double x, double y, int iy) {
    */
-  r = T[1] + w * (T[3] + w * (T[5] + w * (T[7] + w * (T[9] + w * T[11]))));
-  v = z *
-      (T[2] + w * (T[4] + w * (T[6] + w * (T[8] + w * (T[10] + w * T[12])))));
+  r = PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, T[11], T[9]), T[7]), T[5]), T[3]), T[1]);
+  /* v = z * (T[2] + w * (...)): the product with z is fused into r + v below. */
+  v = PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, T[12], T[10]), T[8]), T[6]), T[4]), T[2]);
   s = z * x;
-  r = y + z * (s * (r + v) + y);
-  r += T[0] * s;
+  r = PTS_V8_FMA(z, PTS_V8_FMA(s, PTS_V8_FMA(z, v, r), y), y); /* y + z * (s * (r + v) + y) */
+  r = PTS_V8_FMA(T[0], s, r);                                   /* r += T[0] * s */
   w = x + r;
@@ -841,3 +841,3 @@ static double __kernel_tan(double x, double y, int iy) {
     v = iy;
-    return (1 - ((hx >> 30) & 2)) * (v - 2.0 * (x - (w * w / (w + v) - r)));
+    return (1 - ((hx >> 30) & 2)) * PTS_V8_FMA(-2.0, x - (w * w / (w + v) - r), v);
   }
@@ -857,4 +857,4 @@ static double __kernel_tan(double x, double y, int iy) {
     SET_LOW_WORD(t, 0);
-    s = 1.0 + t * z;
-    return t + a * (s + t * v);
+    s = PTS_V8_FMA(t, z, 1.0);
+    return PTS_V8_FMA(a, PTS_V8_FMA(t, v, s), t);
   }
@@ -927,14 +927,14 @@ double pts_ieee754_acos(double x) {
     z = x * x;
-    p = z * (pS0 + z * (pS1 + z * (pS2 + z * (pS3 + z * (pS4 + z * pS5)))));
-    q = one + z * (qS1 + z * (qS2 + z * (qS3 + z * qS4)));
+    p = z * PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, pS5, pS4), pS3), pS2), pS1), pS0);
+    q = PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, qS4, qS3), qS2), qS1), one);
     r = p / q;
-    return pio2_hi - (x - (pio2_lo - x * r));
+    return pio2_hi - (x - PTS_V8_FMA(-x, r, pio2_lo));
   } else if (hx < 0) { /* x < -0.5 */
     z = (one + x) * 0.5;
-    p = z * (pS0 + z * (pS1 + z * (pS2 + z * (pS3 + z * (pS4 + z * pS5)))));
-    q = one + z * (qS1 + z * (qS2 + z * (qS3 + z * qS4)));
+    p = z * PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, pS5, pS4), pS3), pS2), pS1), pS0);
+    q = PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, qS4, qS3), qS2), qS1), one);
     s = sqrt(z);
     r = p / q;
-    w = r * s - pio2_lo;
-    return pi - 2.0 * (s + w);
+    w = PTS_V8_FMA(r, s, -pio2_lo);
+    return PTS_V8_FMA(-2.0, s + w, pi);
   } else { /* x > 0.5 */
@@ -944,7 +944,7 @@ double pts_ieee754_acos(double x) {
     SET_LOW_WORD(df, 0);
-    c = (z - df * df) / (s + df);
-    p = z * (pS0 + z * (pS1 + z * (pS2 + z * (pS3 + z * (pS4 + z * pS5)))));
-    q = one + z * (qS1 + z * (qS2 + z * (qS3 + z * qS4)));
+    c = PTS_V8_FMA(-df, df, z) / (s + df);
+    p = z * PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, pS5, pS4), pS3), pS2), pS1), pS0);
+    q = PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, qS4, qS3), qS2), qS1), one);
     r = p / q;
-    w = r * s + c;
+    w = PTS_V8_FMA(r, s, c);
     return 2.0 * (df + w);
@@ -985,7 +985,7 @@ double pts_ieee754_acosh(double x) {
   } else if (hx > 0x40000000) { /* 2**28 > x > 2 */
-    t = x * x;
-    return pts_ieee754_log(2.0 * x - one / (x + sqrt(t - one)));
+    /* t = x * x is fused into t - one */
+    return pts_ieee754_log(2.0 * x - one / (x + sqrt(PTS_V8_FMA(x, x, -one))));
   } else { /* 1<x<2 */
     t = x - one;
-    return pts_ieee754_log1p(t + sqrt(2.0 * t + t * t));
+    return pts_ieee754_log1p(t + sqrt(PTS_V8_FMA(2.0, t, t * t)));
   }
@@ -1050,3 +1050,3 @@ double pts_ieee754_asin(double x) {
     if (((ix - 0x3FF00000) | lx) == 0) { /* pts_ieee754_asin(1)=+-pi/2 with inexact */
-      return x * pio2_hi + x * pio2_lo;
+      return PTS_V8_FMA(x, pio2_hi, x * pio2_lo);
     }
@@ -1059,6 +1059,6 @@ double pts_ieee754_asin(double x) {
     }
-    p = t * (pS0 + t * (pS1 + t * (pS2 + t * (pS3 + t * (pS4 + t * pS5)))));
-    q = one + t * (qS1 + t * (qS2 + t * (qS3 + t * qS4)));
+    p = t * PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, pS5, pS4), pS3), pS2), pS1), pS0);
+    q = PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, qS4, qS3), qS2), qS1), one);
     w = p / q;
-    return x + x * w;
+    return PTS_V8_FMA(x, w, x);
   }
@@ -1067,4 +1067,4 @@ double pts_ieee754_asin(double x) {
   t = w * 0.5;
-  p = t * (pS0 + t * (pS1 + t * (pS2 + t * (pS3 + t * (pS4 + t * pS5)))));
-  q = one + t * (qS1 + t * (qS2 + t * (qS3 + t * qS4)));
+  p = t * PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, pS5, pS4), pS3), pS2), pS1), pS0);
+  q = PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, qS4, qS3), qS2), qS1), one);
   s = sqrt(t);
@@ -1072,3 +1072,3 @@ double pts_ieee754_asin(double x) {
     w = p / q;
-    t = pio2_hi - (2.0 * (s + s * w) - pio2_lo);
+    t = pio2_hi - PTS_V8_FMA(2.0, PTS_V8_FMA(s, w, s), -pio2_lo);
   } else {
@@ -1076,6 +1076,6 @@ double pts_ieee754_asin(double x) {
     SET_LOW_WORD(w, 0);
-    c = (t - w * w) / (s + w);
+    c = PTS_V8_FMA(-w, w, t) / (s + w);
     r = p / q;
-    p = 2.0 * s * r - (pio2_lo - 2.0 * c);
-    q = pio4_hi - 2.0 * w;
+    p = PTS_V8_FMA(2.0 * s, r, -PTS_V8_FMA(-2.0, c, pio2_lo)); /* 2.0 * s * r - (pio2_lo - 2.0 * c) */
+    q = PTS_V8_FMA(-2.0, w, pio4_hi);
     t = pio4_hi - (p - q);
@@ -1115,3 +1115,3 @@ double pts_ieee754_asinh(double x) {
     t = fabs(x);
-    w = pts_ieee754_log(2.0 * t + one / (sqrt(x * x + one) + t));
+    w = pts_ieee754_log(2.0 * t + one / (sqrt(PTS_V8_FMA(x, x, one)) + t));
   } else { /* 2.0 > |x| > 2**-28 */
@@ -1202,3 +1202,3 @@ double pts_ieee754_atan(double x) {
         id = 0;
-        x = (2.0 * x - one) / (2.0 + x);
+        x = PTS_V8_FMA(2.0, x, -one) / (2.0 + x);
       } else { /* 11/16<=|x|< 19/16 */
@@ -1210,3 +1210,3 @@ double pts_ieee754_atan(double x) {
         id = 2;
-        x = (x - 1.5) / (one + 1.5 * x);
+        x = (x - 1.5) / PTS_V8_FMA(1.5, x, one);
       } else { /* 2.4375 <= |x| < 2^66 */
@@ -1221,9 +1221,9 @@ double pts_ieee754_atan(double x) {
   /* break sum from i=0 to 10 aT[i]z**(i+1) into odd and even poly */
-  s1 = z * (aT[0] +
-            w * (aT[2] + w * (aT[4] + w * (aT[6] + w * (aT[8] + w * aT[10])))));
-  s2 = w * (aT[1] + w * (aT[3] + w * (aT[5] + w * (aT[7] + w * aT[9]))));
+  /* s1 = z * (aT[0] + w * (...)): the product with z is fused into s1 + s2 below. */
+  s1 = PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, aT[10], aT[8]), aT[6]), aT[4]), aT[2]), aT[0]);
+  s2 = w * PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, aT[9], aT[7]), aT[5]), aT[3]), aT[1]);
   if (id < 0) {
-    return x - x * (s1 + s2);
+    return x - x * PTS_V8_FMA(z, s1, s2);
   } else {
-    z = atanhi[id] - ((x * (s1 + s2) - atanlo[id]) - x);
+    z = atanhi[id] - ((x * PTS_V8_FMA(z, s1, s2) - atanlo[id]) - x);
     return (hx < 0) ? -z : z;
@@ -1332,3 +1332,3 @@ double pts_ieee754_atan2(double y, double x) {
   if (k > 60) { /* |y/x| >  2**60 */
-    z = pi_o_2 + 0.5 * pi_lo;
+    z = PTS_V8_FMA(0.5, pi_lo, pi_o_2);
     m &= 1;
@@ -1530,5 +1530,5 @@ double pts_ieee754_exp(double x) {
     } else {
-      k = (int)(invln2 * x + halF[xsb]);
+      k = (int)PTS_V8_FMA(invln2, x, halF[xsb]);
       t = k;
-      hi = x - t * ln2HI[0]; /* t*ln2HI is exact here */
+      hi = PTS_V8_FMA(-t, ln2HI[0], x); /* x - t * ln2HI[0]; t*ln2HI is exact here */
       lo = t * ln2LO[0];
@@ -1552,3 +1552,3 @@ double pts_ieee754_exp(double x) {
   }
-  c = x - t * (P1 + t * (P2 + t * (P3 + t * (P4 + t * P5))));
+  c = PTS_V8_FMA(-t, PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, PTS_V8_FMA(t, P5, P4), P3), P2), P1), x);
   if (k == 0) {
@@ -1710,6 +1710,6 @@ double pts_ieee754_log(double x) {
         dk = (double)(k);
-        return dk * ln2_hi + dk * ln2_lo;
+        return PTS_V8_FMA(dk, ln2_hi, dk * ln2_lo);
       }
     }
-    R = f * f * (0.5 - 0.33333333333333333 * f);
+    R = f * f * PTS_V8_FMA(-0.33333333333333333, f, 0.5);
     if (k == 0) {
@@ -1718,3 +1718,3 @@ double pts_ieee754_log(double x) {
       dk = (double)(k);
-      return dk * ln2_hi - ((R - dk * ln2_lo) - f);
+      return PTS_V8_FMA(dk, ln2_hi, -(PTS_V8_FMA(-dk, ln2_lo, R) - f)); /* dk * ln2_hi - ((R - dk * ln2_lo) - f) */
     }
@@ -1727,6 +1727,7 @@ double pts_ieee754_log(double x) {
   j = 0x6B851 - hx;
-  t1 = w * (Lg2 + w * (Lg4 + w * Lg6));
-  t2 = z * (Lg1 + w * (Lg3 + w * (Lg5 + w * Lg7)));
+  /* t1 = w * (Lg2 + ...): the product with w is fused into R = t2 + t1 below. */
+  t1 = PTS_V8_FMA(w, PTS_V8_FMA(w, Lg6, Lg4), Lg2);
+  t2 = z * PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, Lg7, Lg5), Lg3), Lg1);
   i |= j;
-  R = t2 + t1;
+  R = PTS_V8_FMA(w, t1, t2);
   if (i > 0) {
@@ -1735,4 +1736,4 @@ double pts_ieee754_log(double x) {
       return f - (hfsq - s * (hfsq + R));
-    else
-      return dk * ln2_hi - ((hfsq - (s * (hfsq + R) + dk * ln2_lo)) - f);
+    else /* dk * ln2_hi - ((hfsq - (s * (hfsq + R) + dk * ln2_lo)) - f) */
+      return PTS_V8_FMA(dk, ln2_hi, -((hfsq - PTS_V8_FMA(dk, ln2_lo, s * (hfsq + R))) - f));
   } else {
@@ -1740,4 +1741,4 @@ double pts_ieee754_log(double x) {
       return f - s * (f - R);
-    else
-      return dk * ln2_hi - ((s * (f - R) - dk * ln2_lo) - f);
+    else /* dk * ln2_hi - ((s * (f - R) - dk * ln2_lo) - f) */
+      return PTS_V8_FMA(dk, ln2_hi, -(PTS_V8_FMA(-dk, ln2_lo, s * (f - R)) - f));
   }
@@ -1843,3 +1844,3 @@ double pts_ieee754_log1p(double x) {
       else
-        return x - x * x * 0.5;
+        return PTS_V8_FMA(-(x * x), 0.5, x);
     }
@@ -1888,11 +1889,11 @@ double pts_ieee754_log1p(double x) {
       } else {
-        c += k * ln2_lo;
-        return k * ln2_hi + c;
+        c = PTS_V8_FMA(k, ln2_lo, c);
+        return PTS_V8_FMA(k, ln2_hi, c);
       }
     }
-    R = hfsq * (1.0 - 0.66666666666666666 * f);
+    R = hfsq * PTS_V8_FMA(-0.66666666666666666, f, 1.0);
     if (k == 0)
       return f - R;
-    else
-      return k * ln2_hi - ((R - (k * ln2_lo + c)) - f);
+    else /* k * ln2_hi - ((R - (k * ln2_lo + c)) - f) */
+      return PTS_V8_FMA(k, ln2_hi, -((R - PTS_V8_FMA(k, ln2_lo, c)) - f));
   }
@@ -1900,8 +1901,8 @@ double pts_ieee754_log1p(double x) {
   z = s * s;
-  R = z * (Lp1 +
-           z * (Lp2 + z * (Lp3 + z * (Lp4 + z * (Lp5 + z * (Lp6 + z * Lp7))))));
+  /* R = z * (Lp1 + z * (...)): the product with z is fused into hfsq + R below. */
+  R = PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, PTS_V8_FMA(z, Lp7, Lp6), Lp5), Lp4), Lp3), Lp2), Lp1);
   if (k == 0)
-    return f - (hfsq - s * (hfsq + R));
-  else
-    return k * ln2_hi - ((hfsq - (s * (hfsq + R) + (k * ln2_lo + c))) - f);
+    return f - (hfsq - s * PTS_V8_FMA(z, R, hfsq));
+  else /* k * ln2_hi - ((hfsq - (s * (hfsq + R) + (k * ln2_lo + c))) - f) */
+    return PTS_V8_FMA(k, ln2_hi, -((hfsq - (s * PTS_V8_FMA(z, R, hfsq) + PTS_V8_FMA(k, ln2_lo, c))) - f));
 }
@@ -1976,5 +1977,8 @@ static const double Lg1 = 6.666666666666735130e-01, /* 3FE55555 55555593 */
  * substantial performance improvement (~40% on amd64).
+ *
+ * k_log1p(f) is s * (hfsq + R). Inlined into log2, its only caller, the product is fused into the
+ * sum it is added to, so this returns hfsq + R and sets *s; log2 multiplies.
  */
-static inline double k_log1p(double f) {
-  double hfsq, s, z, R, w, t1, t2;
+static inline double k_log1p_sum(double f, double *s_out) {
+  double s, z, R, w, t1, t2;
 
@@ -1983,7 +1987,8 @@ static inline double k_log1p(double f) {
   w = z * z;
-  t1 = w * (Lg2 + w * (Lg4 + w * Lg6));
-  t2 = z * (Lg1 + w * (Lg3 + w * (Lg5 + w * Lg7)));
-  R = t2 + t1;
-  hfsq = 0.5 * f * f;
-  return s * (hfsq + R);
+  /* t1 = w * (Lg2 + ...): the product with w is fused into R = t2 + t1 below. */
+  t1 = PTS_V8_FMA(w, PTS_V8_FMA(w, Lg6, Lg4), Lg2);
+  t2 = z * PTS_V8_FMA(w, PTS_V8_FMA(w, PTS_V8_FMA(w, Lg7, Lg5), Lg3), Lg1);
+  R = PTS_V8_FMA(w, t1, t2);
+  *s_out = s;
+  return PTS_V8_FMA(0.5 * f, f, R); /* hfsq + R, hfsq = 0.5 * f * f */
 }
@@ -2005,3 +2010,3 @@ double pts_ieee754_log2(double x) {
 
-  double f, hfsq, hi, lo, r, val_hi, val_lo, w, y;
+  double f, half_f, s, r_sum, hi, lo, val_lo, w, y;
   int32_t i, k, hx;
@@ -2032,4 +2037,5 @@ double pts_ieee754_log2(double x) {
   f = x - 1.0;
-  hfsq = 0.5 * f * f;
-  r = k_log1p(f);
+  /* hfsq = 0.5 * f * f is fused into each of its uses; r = k_log1p(f) = s * r_sum into lo's sum. */
+  half_f = 0.5 * f;
+  r_sum = k_log1p_sum(f, &s);
 
@@ -2065,14 +2071,13 @@ double pts_ieee754_log2(double x) {
    */
-  hi = f - hfsq;
+  hi = PTS_V8_FMA(-half_f, f, f); /* f - hfsq */
   SET_LOW_WORD(hi, 0);
-  lo = (f - hi) - hfsq + r;
-  val_hi = hi * ivln2hi;
-  val_lo = (lo + hi) * ivln2lo + lo * ivln2hi;
+  lo = PTS_V8_FMA(s, r_sum, PTS_V8_FMA(-half_f, f, f - hi)); /* (f - hi) - hfsq + r */
+  /* val_hi = hi * ivln2hi is fused into both of its uses. */
+  val_lo = PTS_V8_FMA(lo + hi, ivln2lo, lo * ivln2hi);
 
   /* spadd(val_hi, val_lo, y), except for not using double_t: */
-  w = y + val_hi;
-  val_lo += (y - w) + val_hi;
-  val_hi = w;
+  w = PTS_V8_FMA(hi, ivln2hi, y);           /* y + val_hi */
+  val_lo += PTS_V8_FMA(hi, ivln2hi, y - w); /* (y - w) + val_hi */
 
-  return val_lo + val_hi;
+  return val_lo + w;
 }
@@ -2141,4 +2146,4 @@ double pts_ieee754_log10(double x) {
 
-  double z = y * log10_2lo + ivln10 * pts_ieee754_log(x);
-  return z + y * log10_2hi;
+  double z = PTS_V8_FMA(y, log10_2lo, ivln10 * pts_ieee754_log(x));
+  return PTS_V8_FMA(y, log10_2hi, z);
 }
@@ -2299,3 +2304,3 @@ double pts_ieee754_expm1(double x) {
       t = k;
-      hi = x - t * ln2_hi; /* t*ln2_hi is exact here */
+      hi = PTS_V8_FMA(-t, ln2_hi, x); /* x - t * ln2_hi; t*ln2_hi is exact here */
       lo = t * ln2_lo;
@@ -2314,7 +2319,7 @@ double pts_ieee754_expm1(double x) {
   hxs = x * hfx;
-  r1 = one + hxs * (Q1 + hxs * (Q2 + hxs * (Q3 + hxs * (Q4 + hxs * Q5))));
-  t = 3.0 - r1 * hfx;
-  e = hxs * ((r1 - t) / (6.0 - x * t));
+  r1 = PTS_V8_FMA(hxs, PTS_V8_FMA(hxs, PTS_V8_FMA(hxs, PTS_V8_FMA(hxs, PTS_V8_FMA(hxs, Q5, Q4), Q3), Q2), Q1), one);
+  t = PTS_V8_FMA(-r1, hfx, 3.0);
+  e = hxs * ((r1 - t) / PTS_V8_FMA(-x, t, 6.0));
   if (k == 0) {
-    return x - (x * e - hxs); /* c is 0 */
+    return x - PTS_V8_FMA(x, e, -hxs); /* x - (x * e - hxs); c is 0 */
   } else {
@@ -2324,5 +2329,5 @@ double pts_ieee754_expm1(double x) {
         0); /* 2^k */
-    e = (x * (e - c) - c);
+    e = PTS_V8_FMA(x, e - c, -c);
     e -= hxs;
-    if (k == -1) return 0.5 * (x - e) - 0.5;
+    if (k == -1) return PTS_V8_FMA(0.5, x - e, -0.5);
     if (k == 1) {
@@ -2331,3 +2336,3 @@ double pts_ieee754_expm1(double x) {
       else
-        return one + 2.0 * (x - e);
+        return PTS_V8_FMA(2.0, x - e, one);
     }
@@ -2372,3 +2377,3 @@ double pts_ieee754_cbrt(double x) {
   int32_t hx;
-  double r, s, t = 0.0, w;
+  double r, s, t = 0.0;
   uint32_t sign;
@@ -2417,3 +2422,3 @@ double pts_ieee754_cbrt(double x) {
   r = (t * t) * (t / x);
-  t = t * ((P0 + r * (P1 + r * P2)) + ((r * r) * r) * (P3 + r * P4));
+  t = t * PTS_V8_FMA((r * r) * r, PTS_V8_FMA(r, P4, P3), PTS_V8_FMA(r, PTS_V8_FMA(r, P2, P1), P0));
 
@@ -2434,7 +2439,7 @@ double pts_ieee754_cbrt(double x) {
   /* one step Newton iteration to 53 bits with error < 0.667 ulps */
-  s = t * t;             /* t*t is exact */
-  r = x / s;             /* error <= 0.5 ulps; |r| < |t| */
-  w = t + t;             /* t+t is exact */
-  r = (r - t) / (w + r); /* r-t is exact; w+r ~= 3*t */
-  t = t + t * r;         /* error <= 0.5 + 0.5/3 + epsilon */
+  s = t * t; /* t*t is exact */
+  r = x / s; /* error <= 0.5 ulps; |r| < |t| */
+  /* w = t + t is exact; GCC makes it 2.0 * t and fuses it into w + r */
+  r = (r - t) / PTS_V8_FMA(2.0, t, r); /* r-t is exact; w+r ~= 3*t */
+  t = PTS_V8_FMA(t, r, t);              /* t + t * r; error <= 0.5 + 0.5/3 + epsilon */
 
@@ -2599,3 +2604,3 @@ double pts_ieee754_cosh(double x) {
     double t = pts_ieee754_exp(fabs(x));
-    return half * t + half / t;
+    return PTS_V8_FMA(half, t, half / t);
   }
@@ -2655,3 +2660,3 @@ double pts_ieee754_sinh(double x) {
     if (ax < 1) {
-      return h * (2 * t - t * t / (t + 1));
+      return h * PTS_V8_FMA(2, t, -(t * t / (t + 1)));
     }
diff --git a/pts/runtime/src/number_methods.c b/pts/runtime/src/number_methods.c
--- a/pts/runtime/src/number_methods.c
+++ b/pts/runtime/src/number_methods.c
@@ -9,2 +9,3 @@
 #include "string_internal.h"
+#include "v8_fma_internal.h"
 
@@ -437,3 +438,6 @@ static double power_of_two_digits(const char *s, size_t n, int radix) {
 
-/* The digits in another radix, accumulated in 32-bit parts as V8 does (approximate beyond 2^53, as the spec allows). */
+/*
+ * The digits in another radix, accumulated in 32-bit parts as V8 does (approximate beyond 2^53, as the spec allows),
+ * with each part added as Node's build adds it (fused on arm64 Linux, v8_fma_internal.h).
+ */
 static double other_radix_digits(const char *s, size_t n, int radix) {
@@ -442,2 +446,5 @@ static double other_radix_digits(const char *s, size_t n, int radix) {
   size_t i = 0;
+  while (i < n && s[i] == '0') { /* V8 skips leading zeros before it groups the digits into parts */
+    i++;
+  }
   while (i < n) {
@@ -453,3 +460,3 @@ static double other_radix_digits(const char *s, size_t n, int radix) {
     }
-    result = result * multiplier + part;
+    result = PTS_V8_FMA(result, (double)multiplier, (double)part);
   }
diff --git a/pts/runtime/src/v8_fma_internal.h b/pts/runtime/src/v8_fma_internal.h
new file mode 100644
--- /dev/null
+++ b/pts/runtime/src/v8_fma_internal.h
@@ -0,0 +1,23 @@
+/*
+ * PTS_V8_FMA(a, b, c) is a * b + c, rounded once where Node's own build of V8's C++ rounds it once.
+ *
+ * Node's official linux-arm64 binary is built by GCC 12, whose default -ffp-contract=fast fuses a
+ * product into the addition or subtraction that is its only use in the same basic block (GCC's
+ * widening_mul pass); x86-64's baseline has no FMA instruction, so nothing fuses there, and V8's
+ * JIT never fuses JavaScript's own arithmetic. The runtime's ports of that C++ (Math in ieee754.c,
+ * parseInt's other radixes in number_methods.c, MakeDate in date.c) write PTS_V8_FMA exactly where
+ * GCC 12 fuses and nowhere else, and still compile with -ffp-contract=off, so the C compiler fuses
+ * nothing of its own: an aarch64 Linux program prints what Node prints on arm64 Linux, and an x86-64
+ * one what Node prints on x86-64. c - a * b is PTS_V8_FMA(-a, b, c) and a * b - c is
+ * PTS_V8_FMA(a, b, -c); negation is exact, so the unfused forms round as the original expressions do.
+ */
+#ifndef PTS_V8_FMA_INTERNAL_H
+#define PTS_V8_FMA_INTERNAL_H
+
+#if defined(__aarch64__) && defined(__linux__)
+#define PTS_V8_FMA(a, b, c) __builtin_fma((a), (b), (c))
+#else
+#define PTS_V8_FMA(a, b, c) ((a) * (b) + (c))
+#endif
+
+#endif
```

Without the leading-zero hunk, x86 probe outputs (date, format, hypot_pow, parseint, parse, radix) were byte-identical between HEAD and the prototype; HEAD-x86 = prototype-x86 also holds on all 96M Math inputs. The x86 machine code itself does change (scheduling), but the arithmetic is the same: the unfused macro is the same expression up to commutativity. The only theoretical difference is which NaN payload propagates, which JavaScript can't observe.

## 6. Regression tests (in my worktree, for you to review)

- `pts/tests/testdata/node_arch_numbers.tsv`: 381 cases with columns `expression`, `linux-x64`, `linux-arm64`, recorded from both official Nodes with `TZ=America/New_York`. 369 of them differ between the two architectures. Contents:
  - 211 Math inputs where one fused site alone changes the result (78 sites);
  - 5 per function where the Nodes differ;
  - the CI cases and your 4 residual cases (those print the same on both Nodes);
  - 56 parseInt cases (28 radixes);
  - 18 Date cancellation cases (6 forms);
  - 8 leading-zero parseInt cases, which are the same on both architectures.
- `pts/tests/node_arch_test.go`, `TestNumbersPrintAsNodeOfTheProgramsArchitecture`. The program first prints `process.platform-arch`, then the test compares each line against that column. It needs no Node, so it covers the x86 host, the qemu cross suite (`PTS_TARGET`) and native arm64 CI.
- `cmd/pts/cross_target_test.go`, `TestBuildForAarch64LinuxPrintsNumbersAsNodeOnArm64`. It always builds `--target aarch64-linux`, runs under qemu, and reports differing expressions.
- Generators: `build_golden.py` and `make_golden.py`. `go vet` and `gofmt` are clean.

Results (`run_new_tests.sh`, final run in `out/new_tests_final.txt`):

| run | HEAD runtime | prototype |
|---|---|---|
| pts/tests, x86 | exit 1: 8 lines (only the leading-zero cases) | exit 0 |
| pts/tests, aarch64 | exit 1: 377 lines | exit 0 |
| cmd/pts cross test | exit 1: 377 lines | exit 0 |

## 7. Other findings

1. **Pre-existing bug on both architectures: parseInt doesn't skip leading zeros before grouping digits.** V8 skips them; PTS groups them into the first part. 143 of 3001 x86 probe lines diverge from x86 Node.
   - Reproducer: `parseInt("01877849bg6cba000000000000000000000000", 23)` gives `1.4330844957842193e+49` in Node and `1.4330844957842188e+49` in PTS-x86 HEAD. With `"00…"`, PTS gives `1.433084495784219e+49`.
   - The three-line hunk above fixes it: 0 probe lines remain on either architecture.
2. **Pre-existing, very contrived: `make_day` adds `dt - 1` in a different order from V8.** V8's `MakeDay` returns `double(int day − 1) + DoubleToInteger(date)`; PTS computes `(D + dt) - 1`. It only matters when `|date|` is above 2^53 days and a huge ms cancels it.
   - Reproducer: `Date.UTC(2982, 25, 9007199254741021, 0, 0, 0, -7.782220156096241e+23)` gives `32001398669312` in Node and `32001532887040` in PTS-x86 HEAD (2 of 40 probe lines).
   - Not fixed in the prototype. The fix would be `(day_from_year(ym) + days_before_month[..] - 1) + dt`.
3. **macOS arm64 (later in H36): unverified, there is no Mac.**
   - Clang 18 with no `-ffp-contract` flag, for `arm64-apple-macos13`, emits the same 274 `llvm.fmuladd` as `-ffp-contract=on`. So upstream's default is `on`, which fuses only within one expression. Apple clang presumably does the same.
   - If macOS Node is built that way, it would match variant (c), which differs from Linux arm64 Node on 2681 of the 2.4M inputs, in log, log10, log2, log1p, atan, asinh, acosh, atanh, atan2 and tan.
   - At line level, clang `on` and GCC 12 share 93 lines. GCC 12 alone fuses on lines 252, 260, 1731, 1986, 1988, 2066, 2068, 2073, 2074 and 2438. Clang `on` alone fuses on lines 1228, 1735, 1740 and 2298 (HEAD's line numbers).
   - `#pragma STDC FP_CONTRACT ON` on HEAD's untouched port gives exactly the `-ffp-contract=on` result (c2 = c). It can't be layered onto the rewritten port, because the cross-statement rewrites would become fusible within one expression.
   - For darwin you would need per-platform site flags, for example `PTS_V8_FMA_IF(mask, a, b, c)`, using `clang_on/sites_clang_on.txt`, and checking against a real macOS Node.
4. **Risks.**
   - The site set is GCC 12.3's. Node uses Red Hat's 12.2.1. It agreed on every one of the 98.4M Math inputs (including the asinh site where GCC 13 differs) and on every function's fused-instruction counts.
   - 119 sites are confirmed only by the site list and instruction counts; they never change a result on their own.
   - A future Node built with a different GCC could move sites. The golden test would catch that once the file is re-recorded from the new Node.

Probes are packed in `/tmp/pts-h36/repro/h36-reproducer-probes.tar.gz` (50 MB, 299 entries; `tar tzf` exit 0). It leaves out the 4.8M-per-function set, binaries and caches, listed with sha256 sums in `EXCLUDED.sha256`. If you archive it into the repo, check its size first: about 40 MB of it is the 2.4M inputs and both Nodes' results.

Files are in `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a115bda57afdd6198/`:
- `pts/runtime/src/v8_fma_internal.h`
- `pts/runtime/src/ieee754.c`
- `pts/runtime/src/number_methods.c`
- `pts/runtime/src/date.c`
- `pts/tests/node_arch_test.go`
- `pts/tests/testdata/node_arch_numbers.tsv`
- `cmd/pts/cross_target_test.go`

Patches: `/tmp/pts-h36/repro/patches/{runtime_U1.diff,tests.diff,golden.diff}`.