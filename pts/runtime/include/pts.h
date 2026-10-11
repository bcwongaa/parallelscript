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
#include <gc_mark.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ---------------------------------------------------------------- objects */

typedef enum pts_kind {
  PTS_KIND_STRING = 1,
  PTS_KIND_INT64 = 2,  /* a boxed int64_t (pts_boxed_int64) */
  PTS_KIND_UINT64 = 3, /* a boxed uint64_t (pts_boxed_uint64) */
  PTS_KIND_OBJECT = 4, /* an instance of a class or object type, described by its type's fields */
} pts_kind;

/* What a field of an object holds, so that console.log and JSON can read it. */
typedef enum pts_field_kind {
  PTS_FIELD_NUMBER = 1, /* double */
  PTS_FIELD_BOOL,       /* bool */
  PTS_FIELD_STRING,     /* pts_string * */
  PTS_FIELD_VALUE,      /* pts_value */
  PTS_FIELD_OBJECT,     /* pts_object *, of the field's static type or a subtype */
  PTS_FIELD_I8,
  PTS_FIELD_I16,
  PTS_FIELD_I32,
  PTS_FIELD_I64,
  PTS_FIELD_U8,
  PTS_FIELD_U16,
  PTS_FIELD_U32,
  PTS_FIELD_U64,
  PTS_FIELD_F32,
} pts_field_kind;

typedef struct pts_field {
  const char *name;
  uint32_t offset; /* from the start of the object, header included */
  pts_field_kind kind;
} pts_field;

typedef struct pts_method {
  const char *name;
  void (*function)(void); /* cast to the method's own signature to call it */
} pts_method;

/*
 * Every runtime type has a descriptor, and every heap object starts with a pointer
 * to its type's. The descriptor is also the object's layout for the collector:
 * bdwgc's gcj-style allocation reads gc_descr from the descriptor's second word.
 */
typedef struct pts_type {
  pts_kind kind;
  GC_word gc_descr;              /* which words of an instance may hold pointers (PTS_GC_*) */
  const char *name;              /* the class name, as console.log shows it */
  const struct pts_type *parent; /* the superclass, or NULL */
  uint32_t size;                 /* of an instance in bytes, header included */
  uint32_t field_count;
  const pts_field *fields; /* in declaration order, inherited ones first */
  uint32_t method_count;
  const pts_method *methods;
} pts_type;

_Static_assert(offsetof(pts_type, gc_descr) == sizeof(GC_word), "bdwgc's gcj allocation reads the descriptor from word 1");

/*
 * Mark descriptors for pts_type.gc_descr. PTS_GC_NO_POINTERS: the instance holds no
 * pointers, so it is allocated atomic and never scanned. PTS_GC_POINTER_WORDS(bits):
 * the words whose bit is set (bit i for word i, the header being word 0) may hold
 * pointers; instances of up to 62 words. PTS_GC_ALL_WORDS(size): every word may (any size).
 */
#define PTS_GC_NO_POINTERS ((GC_word)0)
#define PTS_GC_POINTER_WORDS(bits) (((GC_word)__builtin_bitreverse64((uint64_t)(bits))) | GC_DS_BITMAP)
#define PTS_GC_ALL_WORDS(size) ((GC_word)(size) | GC_DS_LENGTH)

typedef struct pts_object {
  const pts_type *type;
} pts_object;

/* A new instance of type with every field zero (0, false, NULL; a pts_value field is 0, not undefined). */
void *pts_object_new(const pts_type *type);

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
/* left + right. A lone high surrogate at the end of left and a lone low one at the start of right join, as in JS. */
pts_string *pts_string_concat(pts_string *left, pts_string *right);
bool pts_string_equals(const pts_string *left, const pts_string *right);

/* Negative, zero or positive as left sorts before, with or after right in UTF-16 code-unit order (JS `<`). */
int pts_string_compare(const pts_string *left, const pts_string *right);

/* Decimal text of a 64-bit integer, every digit exact. */
pts_string *pts_string_from_i64(int64_t value);
pts_string *pts_string_from_u64(uint64_t value);

/*
 * Writes the string as UTF-8 to a C stream (a FILE *, declared as void * so this header
 * needn't include stdio.h). Lone surrogates become U+FFFD, as Node writes them.
 */
size_t pts_string_write_for_output(const pts_string *string, void *out);

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

/* JS's ToBoolean of a number: false for 0, -0 and NaN. */
static inline bool pts_number_truthy(double x) { return x == x && x != 0; }

/*
 * The bitwise operators on numbers: JS converts the operands with ToInt32 (ToUint32
 * for >>>, and the shift count modulo 32) and the int32 or uint32 result back to a number.
 */
static inline int32_t pts_number_to_int32(double d) {
  if (d >= -2147483648.0 && d < 2147483648.0) {
    return (int32_t)d; /* the common case; NaN fails the test */
  }
  if (!__builtin_isfinite(d)) {
    return 0;
  }
  double modulo = __builtin_fmod(__builtin_trunc(d), 4294967296.0);
  if (modulo < 0) {
    modulo += 4294967296.0;
  }
  return (int32_t)(uint32_t)modulo;
}

static inline uint32_t pts_number_shift_count(double count) { return (uint32_t)pts_number_to_int32(count) & 31; }
static inline double pts_number_bitand(double x, double y) { return pts_number_to_int32(x) & pts_number_to_int32(y); }
static inline double pts_number_bitor(double x, double y) { return pts_number_to_int32(x) | pts_number_to_int32(y); }
static inline double pts_number_bitxor(double x, double y) { return pts_number_to_int32(x) ^ pts_number_to_int32(y); }
static inline double pts_number_bitnot(double x) { return ~pts_number_to_int32(x); }
static inline double pts_number_shl(double x, double y) {
  return (int32_t)((uint32_t)pts_number_to_int32(x) << pts_number_shift_count(y));
}
static inline double pts_number_shr(double x, double y) { return pts_number_to_int32(x) >> pts_number_shift_count(y); }
static inline double pts_number_ushr(double x, double y) {
  return (uint32_t)pts_number_to_int32(x) >> pts_number_shift_count(y);
}

/* ---------------------------------------------------------------- integers */

/*
 * The integer types (docs/SPEC.md §3.2) are the C fixed-width types. Arithmetic
 * wraps: it is done in uint64_t, which can't overflow, and converted back, relying
 * on clang's modulo conversion to signed types. Division truncates; dividing by
 * zero throws RangeError, and MIN / -1 wraps to MIN.
 */

_Noreturn void pts_throw_division_by_zero(void);
_Noreturn void pts_throw_negative_exponent(void);

#define PTS_INTEGER_ARITHMETIC(name, T)                                                           \
  static inline T pts_##name##_add(T a, T b) { return (T)((uint64_t)a + (uint64_t)b); }          \
  static inline T pts_##name##_sub(T a, T b) { return (T)((uint64_t)a - (uint64_t)b); }          \
  static inline T pts_##name##_mul(T a, T b) { return (T)((uint64_t)a * (uint64_t)b); }          \
  static inline T pts_##name##_neg(T a) { return (T)(0 - (uint64_t)a); }                         \
  static inline T pts_##name##_pow_unchecked(T base, T exponent) {                               \
    uint64_t result = 1, b = (uint64_t)base, e = (uint64_t)exponent;                             \
    for (; e != 0; e >>= 1, b *= b) {                                                            \
      if (e & 1) {                                                                               \
        result *= b;                                                                             \
      }                                                                                          \
    }                                                                                            \
    return (T)result;                                                                            \
  }

#define PTS_SIGNED_DIVISION(name, T)                                                              \
  static inline T pts_##name##_div(T a, T b) {                                                   \
    if (b == 0) {                                                                                \
      pts_throw_division_by_zero();                                                              \
    }                                                                                            \
    return b == -1 ? pts_##name##_neg(a) : (T)(a / b);                                           \
  }                                                                                              \
  static inline T pts_##name##_rem(T a, T b) {                                                   \
    if (b == 0) {                                                                                \
      pts_throw_division_by_zero();                                                              \
    }                                                                                            \
    return b == -1 ? 0 : (T)(a % b);                                                             \
  }                                                                                              \
  static inline T pts_##name##_pow(T base, T exponent) {                                         \
    if (exponent < 0) {                                                                          \
      pts_throw_negative_exponent();                                                             \
    }                                                                                            \
    return pts_##name##_pow_unchecked(base, exponent);                                           \
  }

#define PTS_UNSIGNED_DIVISION(name, T)                                                            \
  static inline T pts_##name##_div(T a, T b) {                                                   \
    if (b == 0) {                                                                                \
      pts_throw_division_by_zero();                                                              \
    }                                                                                            \
    return (T)(a / b);                                                                           \
  }                                                                                              \
  static inline T pts_##name##_rem(T a, T b) {                                                   \
    if (b == 0) {                                                                                \
      pts_throw_division_by_zero();                                                              \
    }                                                                                            \
    return (T)(a % b);                                                                           \
  }                                                                                              \
  static inline T pts_##name##_pow(T base, T exponent) { return pts_##name##_pow_unchecked(base, exponent); }

/*
 * Shifts on an integer type take the count modulo the type's width, as Java does
 * (D-018); << is done unsigned so it can't overflow, >>> shifts the bits as unsigned.
 */
#define PTS_INTEGER_SHIFTS(name, T, U, BITS)                                                      \
  static inline T pts_##name##_shl(T a, T count) { return (T)((U)a << ((U)count & ((BITS)-1))); } \
  static inline T pts_##name##_shr(T a, T count) { return (T)(a >> ((U)count & ((BITS)-1))); }    \
  static inline T pts_##name##_ushr(T a, T count) { return (T)((U)a >> ((U)count & ((BITS)-1))); }

/* Number (or f32, promoted) to an integer type, as Rust's `as`: truncate, saturate, NaN -> 0. */
#define PTS_NUMBER_TO_SMALL_INTEGER(name, T, MIN, MAX)                                            \
  static inline T pts_number_to_##name(double d) {                                               \
    if (d != d) {                                                                                \
      return 0;                                                                                  \
    }                                                                                            \
    if (d <= (double)(MIN)) {                                                                    \
      return (MIN);                                                                              \
    }                                                                                            \
    if (d >= (double)(MAX)) {                                                                    \
      return (MAX);                                                                              \
    }                                                                                            \
    return (T)d;                                                                                 \
  }

PTS_INTEGER_ARITHMETIC(i8, int8_t)
PTS_INTEGER_ARITHMETIC(i16, int16_t)
PTS_INTEGER_ARITHMETIC(i32, int32_t)
PTS_INTEGER_ARITHMETIC(i64, int64_t)
PTS_INTEGER_ARITHMETIC(u8, uint8_t)
PTS_INTEGER_ARITHMETIC(u16, uint16_t)
PTS_INTEGER_ARITHMETIC(u32, uint32_t)
PTS_INTEGER_ARITHMETIC(u64, uint64_t)
PTS_SIGNED_DIVISION(i8, int8_t)
PTS_SIGNED_DIVISION(i16, int16_t)
PTS_SIGNED_DIVISION(i32, int32_t)
PTS_SIGNED_DIVISION(i64, int64_t)
PTS_UNSIGNED_DIVISION(u8, uint8_t)
PTS_UNSIGNED_DIVISION(u16, uint16_t)
PTS_UNSIGNED_DIVISION(u32, uint32_t)
PTS_UNSIGNED_DIVISION(u64, uint64_t)
PTS_INTEGER_SHIFTS(i8, int8_t, uint8_t, 8)
PTS_INTEGER_SHIFTS(i16, int16_t, uint16_t, 16)
PTS_INTEGER_SHIFTS(i32, int32_t, uint32_t, 32)
PTS_INTEGER_SHIFTS(i64, int64_t, uint64_t, 64)
PTS_INTEGER_SHIFTS(u8, uint8_t, uint8_t, 8)
PTS_INTEGER_SHIFTS(u16, uint16_t, uint16_t, 16)
PTS_INTEGER_SHIFTS(u32, uint32_t, uint32_t, 32)
PTS_INTEGER_SHIFTS(u64, uint64_t, uint64_t, 64)
PTS_NUMBER_TO_SMALL_INTEGER(i8, int8_t, INT8_MIN, INT8_MAX)
PTS_NUMBER_TO_SMALL_INTEGER(i16, int16_t, INT16_MIN, INT16_MAX)
PTS_NUMBER_TO_SMALL_INTEGER(i32, int32_t, INT32_MIN, INT32_MAX)
PTS_NUMBER_TO_SMALL_INTEGER(u8, uint8_t, 0, UINT8_MAX)
PTS_NUMBER_TO_SMALL_INTEGER(u16, uint16_t, 0, UINT16_MAX)
PTS_NUMBER_TO_SMALL_INTEGER(u32, uint32_t, 0, UINT32_MAX)

/* The 64-bit bounds aren't doubles, so they are compared against 2^63 and 2^64. */
static inline int64_t pts_number_to_i64(double d) {
  if (d != d) {
    return 0;
  }
  if (d <= -9223372036854775808.0) {
    return INT64_MIN;
  }
  if (d >= 9223372036854775808.0) {
    return INT64_MAX;
  }
  return (int64_t)d;
}

static inline uint64_t pts_number_to_u64(double d) {
  if (d != d || d <= 0) {
    return 0;
  }
  if (d >= 18446744073709551616.0) {
    return UINT64_MAX;
  }
  return (uint64_t)d;
}

/* f32 `%` and `**`: computed in double (exact for %) and rounded to float. */
static inline float pts_f32_rem(float x, float y) { return (float)pts_number_rem(x, y); }
static inline float pts_f32_pow(float x, float y) { return (float)pts_number_pow(x, y); }

typedef struct pts_boxed_int64 {
  const pts_type *type;
  int64_t value;
} pts_boxed_int64;

typedef struct pts_boxed_uint64 {
  const pts_type *type;
  uint64_t value;
} pts_boxed_uint64;

extern const pts_type pts_int64_type;
extern const pts_type pts_uint64_type;

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

/* 64-bit integers are numbers while a double holds them exactly (|v| <= 2^53), and boxed beyond (D-002). */
pts_value pts_value_from_i64(int64_t value);
pts_value pts_value_from_u64(uint64_t value);

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

/* JS's ToString of a primitive value. */
pts_string *pts_value_to_string(pts_value value);

/* JS's ToBoolean of a primitive value. */
bool pts_value_truthy(pts_value value);

/* typeof of a primitive value: "number" (64-bit integers too), "string", "boolean", "undefined" or "object" (null). */
pts_string *pts_value_typeof(pts_value value);

/* ---------------------------------------------------------------- String.prototype */

/*
 * String.prototype methods, with JS semantics: indices count UTF-16 code units and
 * index arguments go through ToIntegerOrInfinity, so NaN stands for a left-out index
 * that means 0 and Infinity for one that means the length. Results that would exceed
 * the maximum string length throw RangeError: Invalid string length.
 */

/* s[index]: the one-unit string at an integer index in range, or undefined (D-015). */
pts_value pts_string_element(pts_string *string, double index);
/* s[index] where a string is required: throws RangeError for an index out of range. */
pts_string *pts_string_element_checked(pts_string *string, double index);
/* s[index] === other, without making the one-unit string. */
bool pts_string_element_equals(pts_string *string, double index, pts_string *other);

double pts_string_char_code_at(pts_string *string, double index);
pts_string *pts_string_slice(pts_string *string, double start, double end);
pts_string *pts_string_substring(pts_string *string, double start, double end);
double pts_string_index_of(pts_string *string, pts_string *search, double position);
bool pts_string_includes(pts_string *string, pts_string *search, double position);
bool pts_string_starts_with(pts_string *string, pts_string *search, double position);
bool pts_string_ends_with(pts_string *string, pts_string *search, double end_position);
pts_string *pts_string_to_upper_case(pts_string *string);
pts_string *pts_string_to_lower_case(pts_string *string);
pts_string *pts_string_trim(pts_string *string);
pts_string *pts_string_trim_start(pts_string *string);
pts_string *pts_string_trim_end(pts_string *string);
pts_string *pts_string_pad_start(pts_string *string, double max_length, pts_string *fill);
pts_string *pts_string_pad_end(pts_string *string, double max_length, pts_string *fill);
/* Throws RangeError: Invalid count value for a negative or infinite count. */
pts_string *pts_string_repeat(pts_string *string, double count);

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

/* Ends a function that must return a value, after its last statement: reaching it is a compiler bug. */
_Noreturn void pts_unreachable(void);

/* A function's use of a module-scope binding before its declaration has run: JS's ReferenceError. */
_Noreturn void pts_throw_uninitialized(const char *name);

static inline void pts_check_ready(bool ready, const char *name) {
  if (!ready) {
    pts_throw_uninitialized(name);
  }
}

/* ---------------------------------------------------------------- lifecycle */

void pts_runtime_init(int argc, char **argv);

/* Flushes output and returns the process exit code. */
int pts_runtime_exit(int code);

#ifdef __cplusplus
}
#endif

#endif /* PTS_H */
