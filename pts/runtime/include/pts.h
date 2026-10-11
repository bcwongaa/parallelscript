/*
 * The ParallelScript runtime: the one header that generated C includes.
 *
 * Static types compile to native C types (number -> double, boolean -> bool,
 * string -> pts_string *). pts_value is the one-word dynamic value used where
 * the static type is not enough (console.log arguments now; unions and `any`
 * later). See docs/SPEC.md §9 for the representation notes.
 */
#ifndef PTS_H
#define PTS_H

#if !defined(_GNU_SOURCE)
#define _GNU_SOURCE /* libuv and pthread declarations under -std=c11 */
#endif

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#define GC_THREADS
#include <gc.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ---------------------------------------------------------------- objects */

typedef enum pts_kind {
  PTS_KIND_STRING = 1,
} pts_kind;

/* Every runtime type has a descriptor; every heap object starts with a pointer to one. */
typedef struct pts_type {
  pts_kind kind;
  const char *name;
} pts_type;

typedef struct pts_object {
  const pts_type *type;
} pts_object;

/* ---------------------------------------------------------------- strings */

enum { PTS_STRING_ASCII = 1u << 0 };

/* Immutable UTF-8 text. utf16_len is the JS `.length`. bytes is NUL-terminated. */
typedef struct pts_string {
  const pts_type *type;
  uint32_t flags;
  uint32_t byte_len;
  uint32_t utf16_len;
  char bytes[];
} pts_string;

extern const pts_type pts_string_type;

/*
 * A string literal with static storage. The compiler computes the lengths and
 * the ASCII flag. `literal` must be a C string literal of exactly byte_len bytes.
 */
#define PTS_STATIC_STRING(name, flags_, byte_len_, utf16_len_, literal)                          \
  static const struct {                                                                          \
    const pts_type *type;                                                                        \
    uint32_t flags, byte_len, utf16_len;                                                         \
    char bytes[(byte_len_) + 1];                                                                 \
  } name##_storage = {&pts_string_type, (flags_), (byte_len_), (utf16_len_), literal};          \
  static pts_string *const name = (pts_string *)&name##_storage

/* Copies `len` bytes of valid UTF-8 into a new GC string. */
pts_string *pts_string_from_utf8(const char *bytes, size_t len);
pts_string *pts_string_from_cstr(const char *cstr);
pts_string *pts_string_concat(pts_string *left, pts_string *right);
bool pts_string_equals(const pts_string *left, const pts_string *right);

/* ---------------------------------------------------------------- numbers */

/* ECMAScript Number::toString(10): shortest round-trip digits, JS notation rules. */
pts_string *pts_number_to_string(double value);

/* Writes Number::toString(value) and a NUL into buf (at least PTS_NUMBER_BUF_SIZE bytes); returns the length. */
enum { PTS_NUMBER_BUF_SIZE = 32 };
size_t pts_number_format(double value, char *buf);

/*
 * Number operators whose C spelling differs from JS. Generated C must be
 * compiled with -ffp-contract=off so a*b+c is never fused (JS rounds each step).
 * These use clang builtins rather than <math.h>, whose macros would collide
 * with user names in generated C.
 */

/* `x % y`: the remainder of truncating division, with the dividend's sign (exactly C fmod). */
static inline double pts_number_rem(double x, double y) { return __builtin_fmod(x, y); }

/* `x ** y`: C pow, except that JS says NaN for x ** NaN and (+-1) ** +-Infinity. */
static inline double pts_number_pow(double x, double y) {
  if (y != y || (__builtin_isinf(y) && (x == 1 || x == -1))) {
    return __builtin_nan("");
  }
  return __builtin_pow(x, y);
}

/* ---------------------------------------------------------------- dynamic values */

/*
 * One machine word, so a racing reader can never see a torn value (D-L13).
 * Encoding (as in JavaScriptCore), chosen so bdwgc still sees heap pointers:
 *   0x0000_PPPP_PPPP_PPPP  pointer to a pts_object (never below PTS_VALUE_MIN_POINTER)
 *   0x0002_... - 0xFFFC_...  a double, stored as its bits + 2^49 (NaN canonicalised)
 *   small constants        undefined, null, false, true
 */
typedef uint64_t pts_value;

#define PTS_VALUE_DOUBLE_OFFSET (UINT64_C(1) << 49)
#define PTS_VALUE_MIN_POINTER UINT64_C(0x10000)
#define PTS_VALUE_NULL ((pts_value)0x02)
#define PTS_VALUE_FALSE ((pts_value)0x06)
#define PTS_VALUE_TRUE ((pts_value)0x07)
#define PTS_VALUE_UNDEFINED ((pts_value)0x0a)

static inline pts_value pts_value_from_number(double number) {
  uint64_t bits;
  if (number != number) {
    bits = UINT64_C(0x7FF8000000000000);
  } else {
    memcpy(&bits, &number, sizeof bits);
  }
  return bits + PTS_VALUE_DOUBLE_OFFSET;
}

static inline pts_value pts_value_from_bool(bool b) { return b ? PTS_VALUE_TRUE : PTS_VALUE_FALSE; }

static inline pts_value pts_value_from_object(const void *object) { return (pts_value)(uintptr_t)object; }

static inline pts_value pts_value_from_string(const pts_string *string) { return pts_value_from_object(string); }

static inline bool pts_value_is_number(pts_value v) { return v >= PTS_VALUE_DOUBLE_OFFSET; }

static inline bool pts_value_is_object(pts_value v) {
  return v >= PTS_VALUE_MIN_POINTER && v < PTS_VALUE_DOUBLE_OFFSET;
}

static inline double pts_value_as_number(pts_value v) {
  uint64_t bits = v - PTS_VALUE_DOUBLE_OFFSET;
  double number;
  memcpy(&number, &bits, sizeof number);
  return number;
}

static inline pts_object *pts_value_as_object(pts_value v) { return (pts_object *)(uintptr_t)v; }

/* ---------------------------------------------------------------- console */

/* console.log(args...): Node's formatting for primitives, joined by spaces. */
void pts_console_log(size_t argc, const pts_value *argv);

/* ---------------------------------------------------------------- errors */

/*
 * Prints "Uncaught <error_name>: <message>" to stderr and exits with code 1
 * (D-O07). Stands in for thrown errors until exceptions land in M3.
 */
_Noreturn void pts_panic(const char *error_name, const char *format, ...)
    __attribute__((format(printf, 2, 3)));

/* ---------------------------------------------------------------- lifecycle */

void pts_runtime_init(int argc, char **argv);

/* Flushes output and returns the process exit code. */
int pts_runtime_exit(int code);

#ifdef __cplusplus
}
#endif

#endif /* PTS_H */
