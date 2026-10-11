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
  PTS_KIND_ARRAY = 5,  /* an array (pts_array) of elements of its type's element kind */
  PTS_KIND_FUNCTION = 6, /* a function value: a closure (pts_closure); the type's name is the function's */
  PTS_KIND_MAP = 7,      /* a Map (pts_map) from keys of its type's key kind to values of its element kind */
  PTS_KIND_SET = 8,      /* a Set (pts_map) of values of its type's key kind */
  PTS_KIND_TUPLE = 9,    /* a tuple: an object whose fields, named 0, 1, …, are its elements; printed as an array */
  PTS_KIND_RECORD = 10,  /* a hash-map object (pts_map) from string keys to values of its element kind */
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
  /* An accessor property of an object literal (D-045): what console.log shows of it. Field lookups skip these entries. */
  PTS_FIELD_GETTER,
  PTS_FIELD_SETTER,
  PTS_FIELD_GETTER_SETTER,
} pts_field_kind;

/* Whether a field entry stands for an accessor property rather than a value in the object. */
static inline bool pts_field_is_accessor(pts_field_kind kind) { return kind >= PTS_FIELD_GETTER; }

typedef struct pts_field {
  const char *name;
  uint32_t offset; /* from the start of the object, header included */
  pts_field_kind kind;
} pts_field;

/* A function of any type, as method tables hold them: cast it back to its own type to call it. */
typedef void (*pts_function)(void);

typedef enum pts_method_kind {
  PTS_METHOD,
  PTS_GETTER,
  PTS_SETTER,
} pts_method_kind;

typedef struct pts_method {
  const char *name;
  pts_method_kind kind;
  pts_function function; /* takes the object as a pts_object * first (D-036) */
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
  const pts_method *methods; /* what interfaces call: methods, getters and setters, inherited ones included */
  pts_field_kind element;    /* for an array: what its elements are; for a map: what its values are */
  pts_field_kind key;        /* for a map or set: what its keys are */
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
/* The bit of PTS_GC_POINTER_WORDS for the word that holds a member of a struct. */
#define PTS_GC_WORD(type, member) (UINT64_C(1) << (offsetof(type, member) / sizeof(GC_word)))

typedef struct pts_object {
  const pts_type *type;
} pts_object;

/* A new instance of type with every field zero (0, false, NULL; a pts_value field is 0, not undefined). */
void *pts_object_new(const pts_type *type);

/*
 * A place in generated code that reads or writes a property through an interface
 * (D-035) caches the descriptor of the last object it met and the property's offset in
 * it, packed in one word (descriptor | offset << 48; 0 when empty), so that threads racing
 * on the cache never see half an update. Descriptors are static data, far below 2^48.
 */
typedef struct pts_site {
  uint64_t cache;
} pts_site;

#define PTS_SITE_TYPE_MASK ((UINT64_C(1) << 48) - 1)

void *pts_field_address_slow(const pts_object *object, pts_site *site, const char *name);

/* The address of the field of object that holds its property name, found through site's cache. */
static inline void *pts_field_address(const void *object, pts_site *site, const char *name) {
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)((const pts_object *)object)->type == (cache & PTS_SITE_TYPE_MASK)) {
    return (char *)object + (cache >> 48);
  }
  return pts_field_address_slow((const pts_object *)object, site, name);
}

/*
 * A place that calls a method through an interface (D-036) caches the index of the
 * method in the descriptor's method table instead of an offset. A place that reads or
 * writes a property some class provides with accessors caches either the field's offset
 * or, with PTS_SITE_ACCESSOR set, the index of the getter or setter.
 */
#define PTS_SITE_ACCESSOR (UINT64_C(1) << 63)
#define PTS_SITE_INDEX(cache) ((uint32_t)((cache) >> 48) & 0x7fff)

pts_function pts_method_find_slow(const pts_object *object, pts_site *site, const char *name, pts_method_kind kind);

/* The function that implements the method (or getter or setter) name of object, found through site's cache. */
static inline pts_function pts_method_find(const void *object, pts_site *site, const char *name, pts_method_kind kind) {
  const pts_type *type = ((const pts_object *)object)->type;
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)type == (cache & PTS_SITE_TYPE_MASK)) {
    return type->methods[PTS_SITE_INDEX(cache)].function;
  }
  return pts_method_find_slow((const pts_object *)object, site, name, kind);
}

/* Where a property of an object is: a getter or setter that provides it, or else a field. */
typedef struct pts_property {
  pts_function accessor;
  void *field;
} pts_property;

pts_property pts_property_find_slow(const pts_object *object, pts_site *site, const char *name, pts_method_kind kind);

/* Where object's property name is, to read (PTS_GETTER) or write (PTS_SETTER), found through site's cache. */
static inline pts_property pts_property_find(const void *object, pts_site *site, const char *name, pts_method_kind kind) {
  const pts_type *type = ((const pts_object *)object)->type;
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)type == (cache & PTS_SITE_TYPE_MASK)) {
    if (cache & PTS_SITE_ACCESSOR) {
      return (pts_property){type->methods[PTS_SITE_INDEX(cache)].function, NULL};
    }
    return (pts_property){NULL, (char *)object + (cache >> 48)};
  }
  return pts_property_find_slow((const pts_object *)object, site, name, kind);
}

/* Reads, through site, the property name of object, a value of C type T held by a field or a getter. */
#define PTS_GET(T, object, site, name)                                                                     \
  ({                                                                                                       \
    pts_object *pts_get_object = (pts_object *)(object);                                                   \
    pts_property pts_get = pts_property_find(pts_get_object, (site), (name), PTS_GETTER);                  \
    pts_get.accessor != NULL ? ((T(*)(pts_object *))pts_get.accessor)(pts_get_object) : *(T *)pts_get.field; \
  })

/* Writes value, of C type T, to the property name of object through site: to its field, or with its setter. */
#define PTS_SET(T, object, site, name, value)                                                    \
  ({                                                                                             \
    pts_object *pts_set_object = (pts_object *)(object);                                         \
    T pts_set_value = (value);                                                                   \
    pts_property pts_set = pts_property_find(pts_set_object, (site), (name), PTS_SETTER);        \
    if (pts_set.accessor != NULL) {                                                              \
      ((void (*)(pts_object *, T))pts_set.accessor)(pts_set_object, pts_set_value);             \
    } else {                                                                                     \
      *(T *)pts_set.field = pts_set_value;                                                       \
    }                                                                                            \
    pts_set_value;                                                                               \
  })

/*
 * A function value (D-041) is a closure: its descriptor (kind PTS_KIND_FUNCTION, named as
 * the function is in JS; an adapter's has no name), the function's code, then the values
 * the function captures, or the cells of captured variables that may change. The code
 * takes the closure first, then the arguments: cast it to its exact type to call it.
 */
typedef struct pts_closure {
  const pts_type *type;
  pts_function code;
} pts_closure;

/* The parent of the descriptor of a closure that `bind` made of a function value, which it captures first: it shows as "bound " and that function's name. */
extern const pts_type pts_bound_function;

/* A new cell, size bytes and zeroed, for a captured variable that its function and closures share; scanned if it may hold a pointer. */
void *pts_cell_new(size_t size, bool holds_pointers);
/* A new cell holding a copy of cell's value: the next iteration's copy of a loop's `let` variable. */
void *pts_cell_copy(const void *cell, size_t size, bool holds_pointers);

/* Whether object, an instance, is one of type or of a subclass: JS's instanceof on classes. */
static inline bool pts_instance_of(const void *object, const pts_type *type) {
  for (const pts_type *t = ((const pts_object *)object)->type; t != NULL; t = t->parent) {
    if (t == type) {
      return true;
    }
  }
  return false;
}

/* ---------------------------------------------------------------- strings */

enum {
  PTS_STRING_ASCII = 1u << 0,
  PTS_STRING_BUFFERED = 1u << 1, /* bytes is the start of a pts_string_buffer's data */
};

/*
 * Immutable UTF-8 text. utf16_len is the JS `.length`. bytes points at byte_len bytes:
 * right after the header (NUL-terminated), in a static literal (NUL-terminated), or, if
 * PTS_STRING_BUFFERED, at the start of an append buffer that longer strings share.
 */
typedef struct pts_string {
  const pts_type *type;
  uint32_t flags;
  uint32_t byte_len;
  uint32_t utf16_len;
  char *bytes;
} pts_string;

/*
 * An append buffer (M2.7b): the bytes of strings built by concatenation, which are all
 * prefixes of its first `used` bytes. Concatenating onto a string whose bytes end where
 * `used` does claims the next bytes with a compare-and-swap and copies only the new part,
 * so building a string piece by piece is linear. Claimed bytes never change, so racing
 * threads never see a string's bytes change (D-L13).
 */
typedef struct pts_string_buffer {
  size_t capacity;
  size_t used;
  char data[];
} pts_string_buffer;

extern const pts_type pts_string_type;

/*
 * A string literal with static storage. The compiler computes the lengths and
 * the ASCII flag. `literal` must be a C string literal of exactly byte_len bytes.
 */
#define PTS_STATIC_STRING(name, flags_, byte_len_, utf16_len_, literal)                          \
  static const char name##_bytes[(byte_len_) + 1] = literal;                                     \
  static const pts_string name##_storage = {&pts_string_type, (flags_), (byte_len_), (utf16_len_), \
                                            (char *)name##_bytes};                               \
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

/* Math.round: the nearest integer, halves rounded up; -0 for -0.5 <= x < 0, as in JS. */
static inline double pts_math_round(double x) {
  if (x != x || __builtin_isinf(x) || x == 0) {
    return x;
  }
  double rounded = __builtin_floor(x);
  if (x - rounded >= 0.5) {
    rounded += 1;
  }
  return rounded == 0 && x < 0 ? -0.0 : rounded;
}

/* Math.sign: -1, 1, or x itself for -0, 0 and NaN. */
static inline double pts_math_sign(double x) { return x > 0 ? 1 : x < 0 ? -1 : x; }

/* Math.max and Math.min of two numbers: NaN if either is, and +0 above -0. */
static inline double pts_math_max(double x, double y) {
  if (x != x || y != y) {
    return __builtin_nan("");
  }
  if (x == y) {
    return __builtin_signbit(x) ? y : x;
  }
  return x > y ? x : y;
}
static inline double pts_math_min(double x, double y) {
  if (x != x || y != y) {
    return __builtin_nan("");
  }
  if (x == y) {
    return __builtin_signbit(x) ? x : y;
  }
  return x < y ? x : y;
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
/* Throws an error of the class name with message: until exceptions arrive (M3.2), it ends the program as uncaught. */
_Noreturn void pts_throw_error(pts_string *name, pts_string *message);
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
/*
 * What an optional property's field holds when the object leaves the property out
 * (D-065): console.log skips it, `in` tells it apart, and reads see undefined. It is
 * zero, so a new object's fields start out absent, like an array's holes.
 */
#define PTS_VALUE_ABSENT ((pts_value)0)

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

/*
 * Unions (D-062) are pts_values. These read one as the member type the checker narrowed
 * it to: a 64-bit integer is a number while a double holds it exactly, else boxed.
 */
int64_t pts_value_as_i64(pts_value value);
uint64_t pts_value_as_u64(pts_value value);
static inline bool pts_value_is_nullish(pts_value v) { return v == PTS_VALUE_NULL || v == PTS_VALUE_UNDEFINED; }
/* A read of an optional property's field: its value, or undefined if the object left it out. */
static inline pts_value pts_value_present(pts_value v) { return v == PTS_VALUE_ABSENT ? PTS_VALUE_UNDEFINED : v; }
_Noreturn void pts_throw_non_null(pts_value value);
/* `x!`: value, or TypeError if it is null or undefined. */
static inline pts_value pts_value_non_null(pts_value value) {
  if (__builtin_expect(pts_value_is_nullish(value), 0)) {
    pts_throw_non_null(value);
  }
  return value;
}
_Noreturn void pts_throw_not_instance(const pts_type *type);
/*
 * A union narrowed to a class: value, which must be an instance of type or of a subclass.
 * Narrowing by `in`, by a discriminant or by a type guard may be wrong at run time, so
 * this throws TypeError rather than read fields that aren't there (D-067).
 */
static inline pts_object *pts_value_as_instance(pts_value value, const pts_type *type) {
  pts_object *object = pts_value_as_object(value);
  if (__builtin_expect(!pts_value_is_object(value) || (object->type != type && !pts_instance_of(object, type)), 0)) {
    pts_throw_not_instance(type);
  }
  return object;
}
/* instanceof on a union: whether value is an instance of type or of a subclass. */
static inline bool pts_value_instance_of(pts_value value, const pts_type *type) {
  return pts_value_is_object(value) && pts_instance_of(pts_value_as_object(value), type);
}
/* JS's ===: numbers by value (NaN unequal to itself, -0 equal to 0), strings by content, 64-bit integers by value, the rest by identity. */
bool pts_value_strict_equals(pts_value a, pts_value b);
/* JS's ==: === for values of one type, null equal to undefined, and numbers compared with strings, booleans and objects converted to them (D-067). */
bool pts_value_loose_equals(pts_value a, pts_value b);
/* JS's ToString of an object: an array's join, its class's toString() if it has one interfaces can call, else "[object Object]". */
pts_string *pts_object_to_string(const pts_object *object);

/*
 * Optional properties through interfaces (D-065, D-066). An object that an interface
 * with an optional property refers to holds it in a field of the property's type (a
 * pts_value, which may be absent), in a field of the one type besides undefined it may
 * have (a double, say), behind a getter and setter of its type, or not at all. A site
 * caches the first case by offset and getters and setters by index, as property sites do.
 * A read gives the value as a pts_value, absent if the object doesn't have the property.
 */
pts_value pts_optional_get_slow(const pts_object *object, pts_site *site, const char *name);
static inline pts_value pts_optional_get(const void *object, pts_site *site, const char *name) {
  const pts_type *type = ((const pts_object *)object)->type;
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)type == (cache & PTS_SITE_TYPE_MASK)) {
    if (cache & PTS_SITE_ACCESSOR) {
      return ((pts_value(*)(pts_object *))type->methods[PTS_SITE_INDEX(cache)].function)((pts_object *)object);
    }
    return *(const pts_value *)((const char *)object + (cache >> 48));
  }
  return pts_optional_get_slow((const pts_object *)object, site, name);
}

/* Writes value to an optional property: TypeError if the object can't hold it (no such property, or undefined for a field of another type). */
void pts_optional_set_slow(pts_object *object, pts_site *site, const char *name, pts_value value);
static inline void pts_optional_set(void *object, pts_site *site, const char *name, pts_value value) {
  const pts_type *type = ((const pts_object *)object)->type;
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)type == (cache & PTS_SITE_TYPE_MASK)) {
    if (cache & PTS_SITE_ACCESSOR) {
      ((void (*)(pts_object *, pts_value))type->methods[PTS_SITE_INDEX(cache)].function)((pts_object *)object, value);
    } else {
      *(pts_value *)((char *)object + (cache >> 48)) = value;
    }
    return;
  }
  pts_optional_set_slow((pts_object *)object, site, name, value);
}

/*
 * `"name" in object`: whether it has a field of that name that isn't absent, or a
 * method, getter or setter in its method table, which lists those that interfaces may
 * call and those `in` tests (D-067).
 */
bool pts_object_has(const void *object, const char *name);

/* ---------------------------------------------------------------- arrays */

/*
 * An array (D-038) has a length and a buffer holding the buffer's capacity and the
 * elements, each of the C type of its descriptor's element kind (8 bytes or less, stored
 * with one write). Growing replaces the buffer instead of resizing it, and every access
 * checks the index against the length and the capacity of the buffer it read, so threads
 * racing on an array may see stale elements but never read or write out of bounds
 * (D-L13). Buffers start zeroed. Racing pushes can leave a slot below the length that no
 * push wrote: a zero pointer or dynamic value there is a hole, read as out of range, so
 * that no NULL reaches typed code.
 */
typedef struct pts_array_buffer {
  size_t capacity;
  _Alignas(8) unsigned char elements[];
} pts_array_buffer;

typedef struct pts_array {
  const pts_type *type;
  pts_array_buffer *buffer;
  size_t length;
} pts_array;

/* The size of a field or array element of the given kind. */
size_t pts_field_kind_size(pts_field_kind kind);

/* A new array of count elements of the given kind, copied from elements (which may be NULL if count is 0). */
pts_array *pts_array_of(pts_field_kind kind, const void *elements, size_t count);

_Noreturn void pts_array_index_error(const pts_array *array, double index);

static inline size_t pts_array_length_of(const pts_array *array) { return __atomic_load_n(&array->length, __ATOMIC_ACQUIRE); }

static inline double pts_array_length(const pts_array *array) { return (double)pts_array_length_of(array); }

/* The address of element index (a number) of array, of size bytes, or NULL if index isn't one of its elements. */
static inline void *pts_array_slot_or_null(const pts_array *array, double index, size_t size) {
  size_t length = pts_array_length_of(array);
  pts_array_buffer *buffer = __atomic_load_n(&array->buffer, __ATOMIC_ACQUIRE);
  size_t limit = length < buffer->capacity ? length : buffer->capacity;
  if (!(index >= 0 && index < (double)limit) || (double)(size_t)index != index) {
    return NULL;
  }
  return buffer->elements + (size_t)index * size;
}

/* The address of element index of array, which throws RangeError if there is none. */
static inline void *pts_array_slot(const pts_array *array, double index, size_t size) {
  void *slot = pts_array_slot_or_null(array, index, size);
  if (slot == NULL) {
    pts_array_index_error(array, index);
  }
  return slot;
}

/* array[index] as a dynamic value: undefined if index isn't one of its elements, as in JS (D-015). */
pts_value pts_array_element(const pts_array *array, double index);

/* Stores *value in array[index], or appends it if index is the length; RangeError for other indices. */
void pts_array_set(pts_array *array, double index, const void *value);

/* Appends (push) or prepends (unshift) count elements; both return the new length. */
double pts_array_push(pts_array *array, const void *values, size_t count);
double pts_array_unshift(pts_array *array, const void *values, size_t count);
// a.push(...values) and a.unshift(...values), for values a new array of a's type that
// nothing else holds (D-057).
double pts_array_push_all(pts_array *array, const pts_array *values);
double pts_array_unshift_all(pts_array *array, const pts_array *values);

/* Removes the last (pop) or first (shift) element into *out; RangeError if array is empty. */
void pts_array_pop(pts_array *array, void *out);
void pts_array_shift(pts_array *array, void *out);

/* pop and shift as dynamic values: undefined if array is empty, as in JS. */
pts_value pts_array_pop_value(pts_array *array);
pts_value pts_array_shift_value(pts_array *array);

/* array.length = length, which may only shrink it: RangeError otherwise. Returns length. */
double pts_array_set_length(pts_array *array, double length);

/*
 * The methods of M2.4b. Indices follow JS: negative ones count from the end, and the
 * lowering passes 0 or Infinity for one left out. Values of the element type are passed
 * by address. slice, splice, concat and flat make new arrays; the others change array.
 */
pts_array *pts_array_slice(const pts_array *array, double start, double end);
pts_array *pts_array_splice(pts_array *array, double start, double delete_count, const void *items, size_t count);
pts_array *pts_array_copy(const pts_array *array);
void pts_array_append(pts_array *array, const pts_array *other);
double pts_array_index_of(const pts_array *array, const void *value, double from);
double pts_array_last_index_of(const pts_array *array, const void *value, double from);
bool pts_array_includes(const pts_array *array, const void *value, double from);
pts_string *pts_array_join(const pts_array *array, pts_string *separator);
pts_array *pts_array_reverse(pts_array *array);
pts_array *pts_array_sort(pts_array *array);
pts_array *pts_array_flat(const pts_array *array, double depth, pts_field_kind result_kind);
pts_array *pts_array_fill(pts_array *array, const void *value, double start, double end);
/*
 * array.sort(compare): sorts array in place, stably, by the numbers compare returns for
 * pairs of elements (NaN counts as 0). call calls compare with two elements, which it is
 * given the addresses of.
 */
pts_array *pts_array_sort_with(pts_array *array, pts_closure *compare, double (*call)(pts_closure *compare, const void *a, const void *b));

/* Array.from(string): its code points, each a string. */
pts_array *pts_array_from_string(pts_string *string);

/* An element that is a pointer or dynamic value, which throws RangeError for a hole as for an index out of range. */
static inline uint64_t pts_array_get_word(const pts_array *array, double index) {
  uint64_t element = __atomic_load_n((const uint64_t *)pts_array_slot(array, index, sizeof(uint64_t)), __ATOMIC_RELAXED);
  if (element == 0) {
    pts_array_index_error(array, index);
  }
  return element;
}

/* Typed access for generated code: T is the C type of the array's elements; PTS_ARRAY_GET_WORD for pointers and dynamic values. */
#define PTS_ARRAY_GET(T, array, index) (*(T *)pts_array_slot((array), (index), sizeof(T)))
#define PTS_ARRAY_GET_WORD(T, array, index) ((T)pts_array_get_word((array), (index)))
#define PTS_ARRAY_SET(T, array, index, value)       \
  ({                                                \
    T pts_set_value = (value);                      \
    pts_array_set((array), (index), &pts_set_value); \
    pts_set_value;                                  \
  })
#define PTS_ARRAY_POP(T, array)        \
  ({                                   \
    T pts_popped;                      \
    pts_array_pop((array), &pts_popped); \
    pts_popped;                        \
  })
#define PTS_ARRAY_SHIFT(T, array)          \
  ({                                       \
    T pts_shifted;                         \
    pts_array_shift((array), &pts_shifted); \
    pts_shifted;                           \
  })

/* An element or field of the given kind, at slot, as a dynamic value (undefined for a hole). */
pts_value pts_value_of_slot(pts_field_kind kind, const void *slot);
/* Stores value, a value of the slot's kind's type (not undefined), in a slot of that kind. */
void pts_store_value_in_slot(pts_field_kind kind, void *slot, pts_value value);

/* ---------------------------------------------------------------- maps and sets */

/*
 * Map and Set (D-046) are one ordered hash table: entries in insertion order, each a key,
 * a value (unused by a Set) and the key's hash, which is PTS_MAP_DELETED once the entry is
 * deleted; and an open-addressing index from hashes to entries. Keys compare with
 * SameValueZero. Keys and values are held as one word each, zero-extended, and written
 * with one store, and a table is never resized in place: growing, compacting or clearing
 * publishes a new table (release) after filling it, and readers bound every access by the
 * table they loaded, so racing threads may miss updates but never read out of bounds
 * (D-L13).
 */
typedef struct pts_map_entry {
  uint64_t key;
  uint64_t value;
  uint64_t hash;
} pts_map_entry;

#define PTS_MAP_DELETED UINT64_MAX

typedef struct pts_map_table {
  size_t capacity;            /* entries it has room for */
  size_t used;                /* entries appended, deleted ones included */
  size_t index_mask;          /* the index has index_mask + 1 slots */
  struct pts_map_table *next; /* the table that replaced this one, for iterators to follow */
  uint32_t *index;            /* entry number + 1, or 0 for an empty slot */
  pts_map_entry entries[];
} pts_map_table;

typedef struct pts_map {
  const pts_type *type;
  pts_map_table *table;
  size_t size; /* live entries */
} pts_map;

/* An iteration over a map or set, as JS's iterators: it sees entries added meanwhile and skips deleted ones. */
typedef struct pts_map_iterator {
  pts_map *map;
  pts_map_table *table;
  size_t position; /* the next entry to look at in table */
  uint64_t key, value;
} pts_map_iterator;

/* A new, empty map or set of the given type (PTS_KIND_MAP or PTS_KIND_SET). */
pts_map *pts_map_new(const pts_type *type);
/* new Map(map), new Set(set): a copy, in the same order. */
pts_map *pts_map_copy(const pts_type *type, const pts_map *source);
/* new Set(array): the array's elements, each once, in order. */
pts_map *pts_set_of_array(const pts_type *type, const pts_array *array);
static inline double pts_map_size(const pts_map *map) { return (double)__atomic_load_n(&map->size, __ATOMIC_ACQUIRE); }
bool pts_map_has(const pts_map *map, const void *key);
/* Copies the value of key into *value and returns true, or returns false if map has no such key. */
bool pts_map_get(const pts_map *map, const void *key, void *value);
/* map.get(key) as a dynamic value: undefined if map has no such key. */
pts_value pts_map_get_value(const pts_map *map, const void *key);
_Noreturn void pts_map_key_error(void);
/* map.set(key, value), set.add(key) (value NULL): returns map. */
pts_map *pts_map_set(pts_map *map, const void *key, const void *value);
bool pts_map_delete(pts_map *map, const void *key);
/* Sets in map each entry of source, a map of the same type, in its iteration order (JS's key order for a hash-map object): returns map. */
pts_map *pts_map_assign(pts_map *map, pts_map *source);
void pts_map_clear(pts_map *map);
pts_map_iterator pts_map_iterate(pts_map *map);
/* Moves to the next live entry, whose key and value it then holds; false at the end. */
bool pts_map_next(pts_map_iterator *iterator);

/* map.get(key)! for generated code: the value, of C type T, or RangeError if there is none. */
#define PTS_MAP_GET(T, map, key)                     \
  ({                                                 \
    T pts_got;                                       \
    if (!pts_map_get((map), (key), &pts_got)) {      \
      pts_map_key_error();                           \
    }                                                \
    pts_got;                                         \
  })
/* The key and value an iterator holds, as their C types. */
#define PTS_MAP_ITERATOR_KEY(T, iterator) (*(T *)&(iterator).key)
#define PTS_MAP_ITERATOR_VALUE(T, iterator) (*(T *)&(iterator).value)
/* m[k] = v for generated code: sets the entry and is the value, of C type T. */
#define PTS_MAP_PUT(T, map, key, value)     \
  ({                                        \
    T pts_put = (value);                    \
    pts_map_set((map), (key), &pts_put);    \
    pts_put;                                \
  })

/*
 * A hash-map object (D-059: Record<string, T>, index signatures) is a pts_map from
 * strings to values whose descriptor has kind PTS_KIND_RECORD and the name "Object". JS
 * lists an object's keys with the array indices ("0", "17") first, in numeric order, and
 * the others in insertion order; pts_record_iterate iterates in that order. Without
 * array-index keys it is pts_map_iterate; with them, it iterates over a snapshot.
 */
pts_map_iterator pts_record_iterate(pts_map *record);
_Noreturn void pts_record_key_error(const pts_string *key);
/* o[k] for generated code: the value, of C type T, or RangeError if o has no key k. */
#define PTS_RECORD_GET(T, record, key)                 \
  ({                                                   \
    T pts_got;                                         \
    pts_string *const *pts_key = (key);                \
    if (!pts_map_get((record), pts_key, &pts_got)) {   \
      pts_record_key_error(*pts_key);                  \
    }                                                  \
    pts_got;                                           \
  })

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
/* JS's StringToNumber (Number(s)): NaN unless s, without surrounding whitespace, is empty, a decimal literal, Infinity or a 0x, 0o or 0b integer. */
double pts_string_to_number(const pts_string *string);
pts_string *pts_string_pad_start(pts_string *string, double max_length, pts_string *fill);
pts_string *pts_string_pad_end(pts_string *string, double max_length, pts_string *fill);
/* Throws RangeError: Invalid count value for a negative or infinite count. */
pts_string *pts_string_repeat(pts_string *string, double count);
/* split on a string separator: at most limit pieces (Infinity if left out). Returns a string array. */
pts_array *pts_string_split(pts_string *string, pts_string *separator, double limit);

/* ---------------------------------------------------------------- console */

/* console.log(args...): Node's formatting for primitives, joined by spaces. */
void pts_console_log(size_t argc, const pts_value *argv);
// console.log with spread arguments: each argv[i] with spread[i] is an array (boxed as an
// object) whose elements are arguments in its place (D-057).
void pts_console_log_spread(size_t argc, const pts_value *argv, const bool *spread);

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
