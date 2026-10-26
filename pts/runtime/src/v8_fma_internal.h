/*
 * Where Node's own build of V8's C++ rounds a multiply and an add once (C362, D-227).
 *
 * Node's official linux-arm64 binary is built by GCC 12, whose default -ffp-contract=fast fuses
 * a product into each addition or subtraction that uses it, when every use is one and all are in
 * its basic block (GCC's widening_mul pass); x86-64's baseline has no FMA instruction, so nothing
 * fuses there, and V8's JIT never fuses JavaScript's own arithmetic. The runtime's ports of that
 * C++ (Math in ieee754.c, parseInt's other radixes in number_methods.c, MakeDate in date.c)
 * compile with -ffp-contract=off, so the C compiler fuses nothing of its own, and fuse explicitly
 * where GCC 12 does: an aarch64 Linux program prints what Node prints on arm64 Linux, and an
 * x86-64 one what Node prints on x86-64.
 *
 * Node's macOS arm64 build is Apple clang's, whose default -ffp-contract=on fuses a product
 * into an add of the same expression only: ieee754.c compiles V8's own expressions there under
 * `#pragma STDC FP_CONTRACT ON`, which is the same rule (D-228).
 *
 * PTS_V8_GCC_FMA is 1 where the runtime fuses as Node's linux-arm64 build does: ieee754.c's
 * functions then take its `#if PTS_V8_GCC_FMA` forms, where each product GCC 12 fuses is
 * PTS_V8_FMA's, and otherwise V8's own expressions. PTS_V8_FMA(a, b, c) is a * b + c, rounded
 * once on aarch64, where both of Node's builds fuse V8's single expressions of that form
 * (parseInt's, MakeDate's): c - a * b is PTS_V8_FMA(-a, b, c) and a * b - c is
 * PTS_V8_FMA(a, b, -c), since negation is exact.
 */
#ifndef PTS_V8_FMA_INTERNAL_H
#define PTS_V8_FMA_INTERNAL_H

#if defined(__aarch64__) && defined(__linux__)
#define PTS_V8_GCC_FMA 1
#else
#define PTS_V8_GCC_FMA 0
#endif

#if defined(__aarch64__)
#define PTS_V8_FMA(a, b, c) __builtin_fma((a), (b), (c))
#else
#define PTS_V8_FMA(a, b, c) ((a) * (b) + (c))
#endif

#endif
