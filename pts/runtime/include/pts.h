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

#include <setjmp.h>
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
  PTS_KIND_DATE = 11,    /* a Date (pts_date) */
  PTS_KIND_GENERATOR = 12, /* a generator object (pts_iterator, D-109) */
  PTS_KIND_PROMISE = 13,   /* a promise (pts_promise, D-112) */
  PTS_KIND_TYPED_ARRAY = 14,  /* a typed array (pts_typed_array, D-125) of elements of its type's element kind */
  PTS_KIND_ARRAY_BUFFER = 15, /* an ArrayBuffer (pts_arraybuffer, D-125) */
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

struct pts_check;

typedef struct pts_field {
  const char *name;
  uint32_t offset; /* from the start of the object, header included */
  pts_field_kind kind;
  /* The field's declared type, for writes and conversions through `any` (D-072); NULL for the type its kind holds (any for PTS_FIELD_VALUE). */
  const struct pts_check *check;
} pts_field;

/* A function of any type, as method tables hold them: cast it back to its own type to call it. */
typedef void (*pts_function)(void);

typedef enum pts_method_kind {
  PTS_METHOD,
  PTS_GETTER,
  PTS_SETTER,
} pts_method_kind;

struct pts_object;
struct pts_closure;

/*
 * Dynamic entries (D-074), for calls through `any`: they take the arguments as dynamic
 * values, convert each to its parameter's type (TypeError if it isn't one; a missing
 * argument is undefined) and return the result as a dynamic value (undefined for none).
 */
typedef uint64_t (*pts_dynamic_method)(struct pts_object *self, uint32_t argc, const uint64_t *argv);
typedef uint64_t (*pts_dynamic_function)(struct pts_closure *self, uint32_t argc, const uint64_t *argv);

typedef struct pts_method {
  const char *name;
  pts_method_kind kind;
  pts_function function; /* takes the object as a pts_object * first (D-036) */
  pts_field_kind result; /* what a getter returns, or a setter takes, for reads and writes through `any` (D-070) */
  const struct pts_check *check; /* its declared type, as pts_field.check (D-072) */
  pts_dynamic_method dynamic;    /* a method's dynamic entry, in a program that uses `any` (D-074) */
  const struct pts_check *signature; /* a method's function type, after `this` (D-075) */
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
  /* For an array, map or hash-map object: its elements' (values') declared type, as pts_field.check (D-072). */
  const struct pts_check *check;
  const struct pts_check *key_check; /* for a map or set: its keys' declared type (D-077) */
  pts_dynamic_function call;         /* a closure's dynamic entry, in a program that uses `any` (D-074) */
  const struct pts_check *signature; /* a closure's function type, which its code takes and returns (D-075) */
  uint32_t length;                   /* a closure's function's `length` in JS (D-098) */
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
 * A spread copy takes its source's property order (D-108): the source's descriptor, if it
 * is one of the descriptors of the copy's shape (orders, one per property order).
 */
static inline void pts_object_take_order(pts_object *copy, const pts_object *source, const pts_type *const *orders, uint32_t count) {
  for (uint32_t i = 0; i < count; i++) {
    if (source->type == orders[i]) {
      copy->type = source->type;
      return;
    }
  }
}

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

/*
 * In a program that converts or writes dynamic values (D-072), each site also passes the
 * check of its property's type, so that the runtime can guard what conversions accept
 * when the site meets a new descriptor: a read site checks the field's kind, a write site
 * caches only fields whose declared type is the property's and converts a value written
 * to a field of a narrower declared type, and an optional property's read site checks
 * that the field's declared type implies the property's. Other programs pass NULL.
 */
void *pts_field_address_slow(const pts_object *object, pts_site *site, const char *name, const struct pts_check *check);

/* The address of the field of object that holds its property name, found through site's cache. */
static inline void *pts_field_address(const void *object, pts_site *site, const char *name, const struct pts_check *check) {
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)((const pts_object *)object)->type == (cache & PTS_SITE_TYPE_MASK)) {
    return (char *)object + (cache >> 48);
  }
  return pts_field_address_slow((const pts_object *)object, site, name, check);
}

/* Writes *value, of the C type check's kind holds, to the field of object that holds its property name, through site (D-072). */
void pts_field_set_slow(pts_object *object, pts_site *site, const char *name, const struct pts_check *check, const void *value);

/* Writes value, of C type T, through site to the field that holds the property name of object, whose type check describes (D-072). */
#define PTS_FIELD_SET(T, object, site, name, check, value)                                \
  ({                                                                                      \
    pts_object *pts_fs_object = (pts_object *)(object);                                   \
    T pts_fs_value = (value);                                                             \
    uint64_t pts_fs_cache = __atomic_load_n(&(site)->cache, __ATOMIC_RELAXED);            \
    if ((uintptr_t)pts_fs_object->type == (pts_fs_cache & PTS_SITE_TYPE_MASK)) {          \
      *(T *)((char *)pts_fs_object + (pts_fs_cache >> 48)) = pts_fs_value;                \
    } else {                                                                              \
      pts_field_set_slow(pts_fs_object, (site), (name), (check), &pts_fs_value);           \
    }                                                                                     \
    pts_fs_value;                                                                         \
  })

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

pts_function pts_method_find_adapted_slow(const pts_object *object, pts_site *site, const char *name, const struct pts_check *signature,
                                          pts_function adapter);

/*
 * pts_method_find for a place in a program whose objects may have come through `any`
 * (D-078): an object of an object type may then hold a method of another signature
 * (an erased copy's `Box<any>` as a Container<number>), which the place calls through
 * adapter, a function of the place's signature that calls the method's dynamic entry.
 */
static inline pts_function pts_method_find_adapted(const void *object, pts_site *site, const char *name, const struct pts_check *signature,
                                                   pts_function adapter) {
  const pts_type *type = ((const pts_object *)object)->type;
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)type == (cache & PTS_SITE_TYPE_MASK)) {
    return type->methods[PTS_SITE_INDEX(cache)].function;
  }
  return pts_method_find_adapted_slow((const pts_object *)object, site, name, signature, adapter);
}

/* Where a property of an object is: a getter or setter that provides it, or else a field. */
typedef struct pts_property {
  pts_function accessor;
  void *field;
} pts_property;

/*
 * A write (PTS_SETTER) given check and the value (D-072) that the slow path made itself,
 * converted to a field or setter of a narrower declared type, finds { NULL, NULL }.
 */
pts_property pts_property_find_slow(const pts_object *object, pts_site *site, const char *name, pts_method_kind kind, const struct pts_check *check,
                                    const void *value);

/* Where object's property name is, to read (PTS_GETTER) or write (PTS_SETTER) value, found through site's cache. */
static inline pts_property pts_property_find(const void *object, pts_site *site, const char *name, pts_method_kind kind, const struct pts_check *check,
                                             const void *value) {
  const pts_type *type = ((const pts_object *)object)->type;
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)type == (cache & PTS_SITE_TYPE_MASK)) {
    if (cache & PTS_SITE_ACCESSOR) {
      return (pts_property){type->methods[PTS_SITE_INDEX(cache)].function, NULL};
    }
    return (pts_property){NULL, (char *)object + (cache >> 48)};
  }
  return pts_property_find_slow((const pts_object *)object, site, name, kind, check, value);
}

/* Reads, through site, the property name of object, a value of C type T held by a field or a getter. */
#define PTS_GET(T, object, site, name, check)                                                              \
  ({                                                                                                       \
    pts_object *pts_get_object = (pts_object *)(object);                                                   \
    pts_property pts_get = pts_property_find(pts_get_object, (site), (name), PTS_GETTER, (check), NULL);   \
    pts_get.accessor != NULL ? ((T(*)(pts_object *))pts_get.accessor)(pts_get_object) : *(T *)pts_get.field; \
  })

/* Writes value, of C type T, to the property name of object through site: to its field, or with its setter. */
#define PTS_SET(T, object, site, name, check, value)                                                         \
  ({                                                                                                         \
    pts_object *pts_set_object = (pts_object *)(object);                                                     \
    T pts_set_value = (value);                                                                               \
    pts_property pts_set = pts_property_find(pts_set_object, (site), (name), PTS_SETTER, (check), &pts_set_value); \
    if (pts_set.accessor != NULL) {                                                                          \
      ((void (*)(pts_object *, T))pts_set.accessor)(pts_set_object, pts_set_value);                         \
    } else if (pts_set.field != NULL) {                                                                      \
      *(T *)pts_set.field = pts_set_value;                                                                   \
    }                                                                                                        \
    pts_set_value;                                                                                           \
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
/* The parents of the descriptors of generator and async functions' closures, which console.log shows as `[GeneratorFunction: name]` and `[AsyncFunction: name]`. */
extern const pts_type pts_generator_function;
extern const pts_type pts_async_function;
extern const pts_type pts_async_generator_function;
/* Whether a member's name is a well-known symbol's (`@@iterator` for [Symbol.iterator], D-111): no string key. */
static inline bool pts_name_is_symbol(const char *name) { return name[0] == '@' && name[1] == '@'; }

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
/* Decodes UTF-8 as the WHATWG decoder (and Node) does: each invalid sequence becomes U+FFFD. */
pts_string *pts_string_from_utf8_lossy(const char *bytes, size_t len);
/* The string as UTF-8, lone surrogates as U+FFFD: its own bytes if it has none, else a new buffer (collected); stores the length. */
const char *pts_string_to_utf8(const pts_string *string, size_t *len);
/* encodeURI (component false) and encodeURIComponent of string; URIError for a lone surrogate. */
pts_string *pts_uri_encode(pts_string *string, bool component);
/* decodeURI (component false) and decodeURIComponent of string; URIError for a malformed escape. */
pts_string *pts_uri_decode(pts_string *string, bool component);
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
 * Number.prototype's formatting methods (D-083): toFixed, toExponential and toPrecision
 * round the exact value half up and throw RangeError for digits out of range;
 * pts_number_to_exponential_shortest is toExponential() without an argument.
 * toString(radix) throws RangeError for a radix outside 2..36; toLocaleString is Node's
 * default English format.
 */
pts_string *pts_number_to_fixed(double x, double fraction_digits);
pts_string *pts_number_to_exponential(double x, double fraction_digits);
pts_string *pts_number_to_exponential_shortest(double x);
pts_string *pts_number_to_precision(double x, double precision);
pts_string *pts_number_to_string_radix(double x, double radix);
pts_string *pts_number_to_locale_string(double x);
/* The global parseInt (radix 0 for a left-out one) and parseFloat. */
double pts_parse_int(pts_string *string, double radix);
double pts_parse_float(pts_string *string);
/* toString(radix) of a 64-bit integer, every digit exact. */
pts_string *pts_i64_to_string_radix(int64_t value, double radix);
pts_string *pts_u64_to_string_radix(uint64_t value, double radix);
/* Number.isInteger, isSafeInteger, isFinite and isNaN of a number, and of a dynamic value (false unless it is a number). */
static inline bool pts_number_is_integer(double x) { return __builtin_isfinite(x) && __builtin_trunc(x) == x; }
static inline bool pts_number_is_safe_integer(double x) { return pts_number_is_integer(x) && __builtin_fabs(x) <= 9007199254740991.0; }
static inline bool pts_number_is_finite(double x) { return __builtin_isfinite(x); }
static inline bool pts_number_is_nan(double x) { return x != x; }

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

/* Math's functions as V8 computes them: fdlibm (ieee754.c, D-084). */
double pts_ieee754_acos(double x);
double pts_ieee754_acosh(double x);
double pts_ieee754_asin(double x);
double pts_ieee754_asinh(double x);
double pts_ieee754_atan(double x);
double pts_ieee754_atan2(double y, double x);
double pts_ieee754_atanh(double x);
double pts_ieee754_cbrt(double x);
double pts_ieee754_cos(double x);
double pts_ieee754_cosh(double x);
double pts_ieee754_exp(double x);
double pts_ieee754_expm1(double x);
double pts_ieee754_log(double x);
double pts_ieee754_log10(double x);
double pts_ieee754_log1p(double x);
double pts_ieee754_log2(double x);
double pts_ieee754_sin(double x);
double pts_ieee754_sinh(double x);
double pts_ieee754_tan(double x);
double pts_ieee754_tanh(double x);

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

/* Math.fround, Math.imul and Math.clz32 (D-084). */
static inline double pts_math_fround(double x) { return (double)(float)x; }
static inline double pts_math_imul(double x, double y) {
  return (double)(int32_t)((uint32_t)pts_number_to_int32(x) * (uint32_t)pts_number_to_int32(y));
}
static inline double pts_math_clz32(double x) {
  uint32_t bits = (uint32_t)pts_number_to_int32(x);
  return bits == 0 ? 32 : __builtin_clz(bits);
}
/* Math.max, Math.min and Math.hypot of their arguments as a number array, and Math.random. */
struct pts_array;
double pts_math_max_of(struct pts_array *values);
double pts_math_min_of(struct pts_array *values);
double pts_math_hypot(struct pts_array *values);
double pts_math_random(void);

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
/*
 * What an optional property's field holds when the object leaves the property out
 * (D-065): console.log skips it, `in` tells it apart, and reads see undefined. It is
 * zero, so a new object's fields start out absent, like an array's holes.
 */
#define PTS_VALUE_ABSENT ((pts_value)0)

/* A tuple's length: its elements up to the first optional one left out, which holds
   PTS_VALUE_ABSENT (D-100); every element of a tuple without optional elements. */
static inline size_t pts_tuple_length(const void *tuple) {
  const pts_type *type = ((const pts_object *)tuple)->type;
  for (uint32_t i = 0; i < type->field_count; i++) {
    const pts_field *field = &type->fields[i];
    if (field->kind == PTS_FIELD_VALUE &&
        __atomic_load_n((const pts_value *)((const char *)tuple + field->offset), __ATOMIC_RELAXED) == PTS_VALUE_ABSENT) {
      return i;
    }
  }
  return type->field_count;
}

/* pts_tuple_length of a tuple as a dynamic value. */
double pts_value_tuple_length(pts_value tuple);

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

/* JS's ToString of a value: an object converts as pts_object_to_string does. */
pts_string *pts_value_to_string(pts_value value);

/* JS's ToBoolean of a primitive value. */
bool pts_value_truthy(pts_value value);

/* typeof of a primitive value: "number" (64-bit integers too), "string", "boolean", "undefined" or "object" (null). */
pts_string *pts_value_typeof(pts_value value);
pts_string *pts_value_constructor_name(pts_value value);

/* Number.isInteger, isSafeInteger, isFinite and isNaN of a dynamic value: false unless it is a number (D-083). */
bool pts_value_is_integer(pts_value value);
bool pts_value_is_safe_integer(pts_value value);
bool pts_value_is_finite(pts_value value);
bool pts_value_is_nan(pts_value value);

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
_Noreturn void pts_throw_not_primitive(pts_value value, const char *type_name);
/*
 * An `unknown` narrowed to a primitive type (D-068): checked, as narrowing to a class is,
 * since a type guard may be wrong at run time. A number may be a boxed 64-bit integer.
 */
static inline pts_string *pts_value_as_string(pts_value value) {
  if (__builtin_expect(!pts_value_is_object(value) || pts_value_as_object(value)->type->kind != PTS_KIND_STRING, 0)) {
    pts_throw_not_primitive(value, "string");
  }
  return (pts_string *)pts_value_as_object(value);
}
double pts_value_to_number_checked(pts_value value);
static inline bool pts_value_as_bool(pts_value value) {
  if (__builtin_expect(value != PTS_VALUE_TRUE && value != PTS_VALUE_FALSE, 0)) {
    pts_throw_not_primitive(value, "boolean");
  }
  return value == PTS_VALUE_TRUE;
}
/* instanceof on a union: whether value is an instance of type or of a subclass. */
static inline bool pts_value_instance_of(pts_value value, const pts_type *type) {
  return pts_value_is_object(value) && pts_instance_of(pts_value_as_object(value), type);
}
/* JS's ===: numbers by value (NaN unequal to itself, -0 equal to 0), strings by content, 64-bit integers by value, the rest by identity. */
bool pts_value_strict_equals(pts_value a, pts_value b);
/*
 * Dynamic values (`any`, D-070): reads and writes of properties and elements found at run
 * time (a missing property reads as undefined; writing one an object lacks, or a value
 * its type doesn't allow, throws TypeError), and the operators with JS's conversions.
 */
pts_value pts_dynamic_get(pts_value object, pts_value key);
pts_value pts_dynamic_set(pts_value object, pts_value key, pts_value value);
/* `key in object`: TypeError if object isn't one. */
bool pts_dynamic_has(pts_value object, pts_value key);
/* Array.isArray of a dynamic value: an array or a tuple. */
bool pts_value_is_array(pts_value value);
/*
 * Object.keys, values and entries of a dynamic value (D-077): a string[] of its own
 * enumerable keys in JS's order, an any[] of their values, an any[] of [key, value]
 * any[]s; Object.hasOwn: whether it has the key. TypeError for null and undefined.
 */
pts_value pts_value_keys(pts_value value);
pts_value pts_value_values(pts_value value);
pts_value pts_value_entries(pts_value value);
bool pts_value_has_own(pts_value value, pts_value key);

/* A function value's `name` and `length` in JS (D-098): an adapter's or bound function's are those of the function it wraps, a bound one's name prefixed "bound ". */
pts_string *pts_value_function_name(pts_value function);
double pts_value_function_length(pts_value function);
/* Object.keys of a value as a string[]; Object.assign(target, ...sources); Object.is (SameValue) (D-086). */
struct pts_array *pts_value_key_array(pts_value value);
void pts_object_assign(pts_value target, struct pts_array *sources);
bool pts_value_same_value(pts_value a, pts_value b);
/* Object.freeze marks an object frozen, and Object.isFrozen tests the mark (true for primitives); writes are not stopped (D-105). */
void pts_value_freeze(pts_value value);
bool pts_value_is_frozen(pts_value value);
/*
 * What `for…of` iterates over for a dynamic value (D-078): an array or tuple itself, read
 * live, or a new array of a string's code points, a set's elements or a map's [key,
 * value] entries; TypeError `text is not iterable` for anything else. Its length and
 * element i, boxed; and a new any[] of a dynamic value's items, for spreading.
 */
pts_value pts_value_iterable(pts_value value, const pts_string *text);
double pts_value_iterable_length(pts_value iterable);
pts_value pts_value_iterable_element(pts_value iterable, double i);
struct pts_array *pts_value_to_array(pts_value value, const pts_string *text);
/* callee(argv…) through `any` (D-074): TypeError `text is not a function` if callee isn't one. */
pts_value pts_dynamic_call(pts_value callee, const pts_string *text, uint32_t argc, const pts_value *argv);
/* object[key](argv…) through `any`: a method (a builtin one of a string, number, boolean, array or function, D-076), or a property that holds a function. */
pts_value pts_dynamic_call_method(pts_value object, pts_value key, const pts_string *text, uint32_t argc, const pts_value *argv);
/* Calls the method name of object through its dynamic entry, as a method adapter does (D-078). */
pts_value pts_method_call_dynamic(pts_object *object, const char *name, uint32_t argc, const pts_value *argv);
/* The same with arguments to spread: each argv[i] whose spread[i] is set is an array whose elements are arguments. */
pts_value pts_dynamic_call_spread(pts_value callee, const pts_string *text, uint32_t argc, const pts_value *argv, const bool *spread);
pts_value pts_dynamic_call_method_spread(pts_value object, pts_value key, const pts_string *text, uint32_t argc, const pts_value *argv, const bool *spread);
pts_value pts_value_add(pts_value left, pts_value right);
double pts_value_to_number(pts_value value);
/* ToPrimitive with the number hint, or the default one: an object's valueOf() if its class has one, else its string (D-088). */
pts_value pts_value_to_primitive(pts_value value, bool number_hint);
bool pts_value_less(pts_value left, pts_value right);
bool pts_value_less_equal(pts_value left, pts_value right);
/*
 * Runtime descriptions of the program's types (D-071), for converting dynamic values to
 * them: cgen writes one per type a conversion from `any` or `unknown` reaches.
 */
typedef enum pts_check_kind {
  PTS_CHECK_ANY = 1,  /* any value: any, unknown */
  PTS_CHECK_NUMBER,   /* a number, or a boxed 64-bit integer */
  PTS_CHECK_INTEGER,  /* an integer the integer type of kind `slot` holds exactly */
  PTS_CHECK_STRING,
  PTS_CHECK_BOOLEAN,
  PTS_CHECK_NULL,
  PTS_CHECK_UNDEFINED,
  PTS_CHECK_INSTANCE, /* an instance of class `type` or of a subclass */
  PTS_CHECK_OBJECT,   /* an object with `properties`; dynamic data is copied into an object of descriptor `type` */
  PTS_CHECK_TUPLE,    /* a tuple of descriptor `type`, whose `properties` are its elements; an array is copied into one */
  PTS_CHECK_ARRAY,    /* an array of `element`s */
  PTS_CHECK_RECORD,   /* a hash-map object of descriptor `type` whose values are `element`s */
  PTS_CHECK_MAP,      /* a Map of descriptor `type` */
  PTS_CHECK_SET,      /* a Set of descriptor `type` */
  PTS_CHECK_UNION,    /* one of `members` */
  PTS_CHECK_FUNCTION, /* a closure of this function type (its descriptor's signature); another closure is wrapped by `adapt` */
  PTS_CHECK_OPAQUE,   /* an object of an unknown type: only a place of this type holds one */
} pts_check_kind;

typedef struct pts_check_property {
  const char *name;
  const struct pts_check *check;
  bool optional;
  uint32_t offset; /* of its field in an object of the copy's descriptor */
} pts_check_property;

/* A method of an object type: its name and its function type after `this`, which a method table entry's signature must be. */
typedef struct pts_check_method {
  const char *name;
  const struct pts_check *signature;
} pts_check_method;

typedef struct pts_check {
  pts_check_kind kind;
  pts_field_kind slot; /* how a place of the type holds a value */
  const char *name;    /* the type, for messages */
  const pts_type *type;
  const struct pts_check *element;
  uint32_t count;
  const pts_check_property *properties;
  const struct pts_check *const *members;
  uint32_t method_count;
  const pts_check_method *methods;
  struct pts_closure *(*adapt)(struct pts_closure *function); /* a function type's adapter of a closure of another type (D-075) */
} pts_check;

/*
 * An adapter (D-075): a closure of a function type, written per type, whose code calls
 * `function`, a closure of another type, through its dynamic entry with the arguments
 * boxed, and converts the result. Its own dynamic entry is pts_adapter_call.
 */
typedef struct pts_adapter {
  struct pts_closure closure;
  struct pts_closure *function;
} pts_adapter;
pts_value pts_adapter_call(struct pts_closure *self, uint32_t argc, const pts_value *argv);
/* function(argv…), a closure, through its dynamic entry: TypeError if it has none. */
pts_value pts_dynamic_call_closure(struct pts_closure *function, uint32_t argc, const pts_value *argv);

/* value as a value of the type check describes: itself if it is one, else a copy of it converted to one (dynamic data); TypeError if neither. */
pts_value pts_value_convert(pts_value value, const pts_check *check);
/* pts_value_convert, which reports whether value converts instead of throwing. */
bool pts_value_try_convert(pts_value value, const pts_check *check, pts_value *out);
/* The declared type of a place of the given kind whose check is check: check, or for NULL, the type the kind holds. */
const pts_check *pts_check_declared(pts_field_kind kind, const pts_check *check);
/* Whether two checks describe the same type, and whether every value of type sub is one of type super (D-072). */
bool pts_check_same(const pts_check *a, const pts_check *b);
bool pts_check_implies(const pts_check *sub, const pts_check *super);
/* The value to store for value written to a place of the given kind declared check (D-072): value, or value converted to an object type; TypeError if it doesn't fit. */
pts_value pts_storable(pts_field_kind kind, const pts_check *check, pts_value value, const char *name, const pts_type *type);
/* Calls a setter whose argument is of the given kind with value, which is of its type. */
void pts_call_setter(pts_function setter, pts_field_kind kind, pts_object *object, pts_value value);

/* A value from `any` that isn't of the type expected where it goes. */
_Noreturn void pts_throw_type_mismatch(pts_value value, const char *expected);
bool pts_value_is_integer_of(pts_value value, pts_field_kind kind);
static inline bool pts_value_is_string(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_STRING;
}
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
pts_value pts_optional_get_slow(const pts_object *object, pts_site *site, const char *name, const struct pts_check *check);
static inline pts_value pts_optional_get(const void *object, pts_site *site, const char *name, const struct pts_check *check) {
  const pts_type *type = ((const pts_object *)object)->type;
  uint64_t cache = __atomic_load_n(&site->cache, __ATOMIC_RELAXED);
  if ((uintptr_t)type == (cache & PTS_SITE_TYPE_MASK)) {
    if (cache & PTS_SITE_ACCESSOR) {
      return ((pts_value(*)(pts_object *))type->methods[PTS_SITE_INDEX(cache)].function)((pts_object *)object);
    }
    return *(const pts_value *)((const char *)object + (cache >> 48));
  }
  return pts_optional_get_slow((const pts_object *)object, site, name, check);
}

/* Writes value to an optional property: TypeError if the object can't hold it (no such property, or undefined for a field of another type). */
void pts_optional_set_slow(pts_object *object, pts_site *site, const char *name, const struct pts_check *check, pts_value value);
static inline void pts_optional_set(void *object, pts_site *site, const char *name, const struct pts_check *check, pts_value value) {
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
  pts_optional_set_slow((pts_object *)object, site, name, check, value);
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

/*
 * The runtime's array descriptors, one per element kind: those of arrays whose element
 * type its kind tells (a PTS_FIELD_VALUE array is an `any[]`). A program that converts
 * or writes dynamic values describes its arrays of other element types itself (D-072).
 */
extern const pts_type pts_array_types[];

/* A new array of descriptor type, holding count elements copied from elements (which may be NULL if count is 0). */
pts_array *pts_array_of(const pts_type *type, const void *elements, size_t count);

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

/* Loads element index of array into out (its element kind's size) with one load; false if there is none, or a racing push left a hole there (D-038). */
bool pts_array_load(const pts_array *array, double index, void *out);

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
/* a.at(index): the element at index (from the end if negative) as a dynamic value, or undefined. */
pts_value pts_array_at(const pts_array *array, double index);
/* a.with(index, value): a copy with the element at index (from the end if negative) replaced; RangeError out of range. */
pts_array *pts_array_with(const pts_array *array, double index, const void *value);
/* new Array(length): length holes (zeros of a primitive element kind); RangeError for an invalid length. */
pts_array *pts_array_of_length(const pts_type *type, double length);
/* a.keys() as a number array of the indices. */
pts_array *pts_array_keys(const pts_array *array);
void pts_array_append(pts_array *array, const pts_array *other);
double pts_array_index_of(const pts_array *array, const void *value, double from);
double pts_array_last_index_of(const pts_array *array, const void *value, double from);
bool pts_array_includes(const pts_array *array, const void *value, double from);
pts_string *pts_array_join(const pts_array *array, pts_string *separator);
pts_array *pts_array_reverse(pts_array *array);
pts_array *pts_array_sort(pts_array *array);
pts_array *pts_array_flat(const pts_array *array, double depth, const pts_type *result_type);
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
  int64_t size; /* live entries; racing threads may lose updates and take it below 0, which reads as 0 (D-130) */
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
static inline double pts_map_size(const pts_map *map) {
  int64_t size = __atomic_load_n(&map->size, __ATOMIC_ACQUIRE);
  return size > 0 ? (double)size : 0;
}
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

/* The hash-map object type of Record<string, any>, which JSON.parse makes its objects of (D-080). */
extern const pts_type pts_type_any_record;

/* JSON.parse: the dynamic value text holds, or SyntaxError with V8's message (D-080). */
pts_value pts_json_parse(const pts_string *text);
/* JSON.parse(text, reviver) for a reviver closure of type (key: string, value: any) => any, boxed. */
pts_value pts_json_parse_revived(const pts_string *text, pts_value reviver);
/*
 * JSON.stringify(value, replacer, space), with JS's rules (toJSON, a replacer function or
 * property list, an indent of up to ten spaces or characters, V8's TypeError for a cycle):
 * a string, or undefined for undefined and functions.
 */
pts_value pts_json_stringify(pts_value value, pts_value replacer, pts_value space);

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
/* at(index): the unit at index (from the end if negative) as a one-unit string, or undefined. */
pts_value pts_string_at(pts_string *string, double index);
/* charAt(index): the unit at index as a one-unit string, or "" out of range. */
pts_string *pts_string_char_at(pts_string *string, double index);
/* codePointAt(index): the code point starting at index (a lone surrogate's or a low half's unit), or undefined. */
pts_value pts_string_code_point_at(pts_string *string, double index);
/* lastIndexOf: a NaN position (left out) means the end. */
double pts_string_last_index_of(pts_string *string, pts_string *search, double position);
/* substr(start, length): a left-out length is Infinity. */
pts_string *pts_string_substr(pts_string *string, double start, double length);
/* replace and replaceAll with a string pattern and replacement ($$, $&, $` and $' substituted). */
pts_string *pts_string_replace(pts_string *string, pts_string *search, pts_string *replacement);
pts_string *pts_string_replace_all(pts_string *string, pts_string *search, pts_string *replacement);
bool pts_string_is_well_formed(pts_string *string);
pts_string *pts_string_to_well_formed(pts_string *string);
/* localeCompare: -1, 0 or 1 in the order of Node's default collator (D-082). */
double pts_string_locale_compare(pts_string *string, pts_string *that);
/* String.fromCharCode and String.fromCodePoint of a number array; fromCodePoint throws RangeError for an invalid code point. */
pts_string *pts_string_from_char_codes(pts_array *codes);
pts_string *pts_string_from_code_points(pts_array *code_points);

/* ---------------------------------------------------------------- Date (D-087) */

/* A Date: its time value, milliseconds since the epoch in UTC, or NaN for an invalid date. */
typedef struct pts_date {
  const pts_type *type;
  double time;
} pts_date;

extern const pts_type pts_type_builtin_Date;
typedef pts_date pts_class_builtin_Date;

/* The fields pts_date_get reads and pts_date_set writes from (the UTC ones are the local ones moved by 8). */
enum {
  PTS_DATE_TIME = 0,
  PTS_DATE_TIMEZONE_OFFSET = 1,
  PTS_DATE_FULL_YEAR = 2,
  PTS_DATE_MONTH = 3,
  PTS_DATE_DATE = 4,
  PTS_DATE_DAY = 5,
  PTS_DATE_HOURS = 6,
  PTS_DATE_MINUTES = 7,
  PTS_DATE_SECONDS = 8,
  PTS_DATE_MILLISECONDS = 9,
  PTS_DATE_UTC_FULL_YEAR = 10, /* … PTS_DATE_UTC_MILLISECONDS = 17 */
};

/* The formats of pts_date_format: toString, toDateString, toTimeString, toISOString (RangeError if invalid), toUTCString. */
enum { PTS_DATE_STRING = 0, PTS_DATE_DATE_ONLY = 1, PTS_DATE_TIME_ONLY = 2, PTS_DATE_ISO = 3, PTS_DATE_UTC = 4 };

double pts_date_now(void);
pts_date *pts_date_new_now(void);
pts_date *pts_date_new_time(double time);
pts_date *pts_date_new_string(pts_string *text);
pts_date *pts_date_new_value(pts_value value);
/* new Date(year, month[, date, hours, minutes, seconds, ms]) in local time; argc of them given. */
pts_date *pts_date_new_components(double argc, double year, double month, double date, double hours, double minutes, double seconds, double ms);
/* Date.UTC(…), argc of them given. */
double pts_date_utc(double argc, double year, double month, double date, double hours, double minutes, double seconds, double ms);
/* Date.parse: ISO's format and the common others; NaN if text is none. */
double pts_date_parse(pts_string *text);
double pts_date_get(pts_date *date, double field);
double pts_date_set_time(pts_date *date, double time);
double pts_date_set(pts_date *date, double field, double argc, double a, double b, double c, double d);
pts_string *pts_date_format(pts_date *date, double format);
/* toJSON: the ISO string, or null for an invalid date. */
pts_value pts_date_to_json(pts_date *date);

/* ---------------------------------------------------------------- typed arrays (D-125) */

/*
 * An ArrayBuffer: byte_length bytes, zeroed when made, in the same allocation, which the
 * collector never scans. (pts_array_buffer is something else: an array's storage.)
 */
typedef struct pts_arraybuffer {
  const pts_type *type;
  size_t byte_length;
  _Alignas(16) unsigned char bytes[];
} pts_arraybuffer;

extern const pts_type pts_type_builtin_ArrayBuffer;
typedef pts_arraybuffer pts_class_builtin_ArrayBuffer;

/*
 * A typed array: a view of length elements of its descriptor's element kind in buffer,
 * from byte byte_offset, which data points at. The length never changes, so threads
 * racing on one may see stale elements but never read or write outside it (D-L13).
 * Uint8ClampedArray's elements are U8, as Uint8Array's; only writes tell them apart.
 */
typedef struct pts_typed_array {
  const pts_type *type;
  pts_arraybuffer *buffer;
  unsigned char *data;
  size_t length;
  size_t byte_offset;
} pts_typed_array;

/* X(name, C element type, element kind, conversion of a number to an element) for each typed array. */
#define PTS_TYPED_ARRAYS(X)                                                \
  X(Int8Array, int8_t, PTS_FIELD_I8, (int8_t)pts_number_to_int32)          \
  X(Uint8Array, uint8_t, PTS_FIELD_U8, (uint8_t)pts_number_to_int32)       \
  X(Uint8ClampedArray, uint8_t, PTS_FIELD_U8, pts_number_to_uint8_clamped) \
  X(Int16Array, int16_t, PTS_FIELD_I16, (int16_t)pts_number_to_int32)      \
  X(Uint16Array, uint16_t, PTS_FIELD_U16, (uint16_t)pts_number_to_int32)   \
  X(Int32Array, int32_t, PTS_FIELD_I32, pts_number_to_int32)               \
  X(Uint32Array, uint32_t, PTS_FIELD_U32, (uint32_t)pts_number_to_int32)   \
  X(Float32Array, float, PTS_FIELD_F32, (float))                           \
  X(Float64Array, double, PTS_FIELD_NUMBER, (double))

/* JS's ToUint8Clamp: NaN and negatives are 0, 255 and above 255, the rest rounded half to even. */
static inline uint8_t pts_number_to_uint8_clamped(double d) {
  if (!(d > 0)) {
    return 0;
  }
  return d >= 255 ? 255 : (uint8_t)__builtin_rint(d);
}

_Noreturn void pts_typed_array_index_error(const pts_typed_array *array, double index);

/* Whether index is a whole number below array's length: an index JS reads and writes. */
static inline bool pts_typed_array_has_index(const pts_typed_array *array, double index) {
  return index >= 0 && index < (double)array->length && (double)(size_t)index == index;
}

/* For each typed array X, its descriptor pts_type_builtin_X and its class's struct for generated code. */
#define PTS_TYPED_ARRAY_TYPE(name, ctype, element_kind, convert) \
  extern const pts_type pts_type_builtin_##name;                 \
  typedef pts_typed_array pts_class_builtin_##name;
PTS_TYPED_ARRAYS(PTS_TYPED_ARRAY_TYPE)
#undef PTS_TYPED_ARRAY_TYPE

/* Whether a descriptor is a typed array's. */
static inline bool pts_is_typed_array_type(const pts_type *type) { return type->kind == PTS_KIND_TYPED_ARRAY; }

/* Element i (below the length) of any typed array, as a number. */
static inline double pts_typed_array_as_number(const pts_typed_array *array, size_t i) {
  switch (array->type->element) {
  case PTS_FIELD_I8:
    return ((const int8_t *)array->data)[i];
  case PTS_FIELD_U8:
    return ((const uint8_t *)array->data)[i];
  case PTS_FIELD_I16:
    return ((const int16_t *)array->data)[i];
  case PTS_FIELD_U16:
    return ((const uint16_t *)array->data)[i];
  case PTS_FIELD_I32:
    return ((const int32_t *)array->data)[i];
  case PTS_FIELD_U32:
    return ((const uint32_t *)array->data)[i];
  case PTS_FIELD_F32:
    return ((const float *)array->data)[i];
  default:
    return ((const double *)array->data)[i];
  }
}

/* Stores value, converted as JS converts it for the array's kind, in element i (below the length) of any typed array. */
static inline void pts_typed_array_store(pts_typed_array *array, size_t i, double value) {
  switch (array->type->element) {
  case PTS_FIELD_I8:
    ((int8_t *)array->data)[i] = (int8_t)pts_number_to_int32(value);
    return;
  case PTS_FIELD_U8:
    ((uint8_t *)array->data)[i] = array->type == &pts_type_builtin_Uint8ClampedArray ? pts_number_to_uint8_clamped(value) : (uint8_t)pts_number_to_int32(value);
    return;
  case PTS_FIELD_I16:
    ((int16_t *)array->data)[i] = (int16_t)pts_number_to_int32(value);
    return;
  case PTS_FIELD_U16:
    ((uint16_t *)array->data)[i] = (uint16_t)pts_number_to_int32(value);
    return;
  case PTS_FIELD_I32:
    ((int32_t *)array->data)[i] = pts_number_to_int32(value);
    return;
  case PTS_FIELD_U32:
    ((uint32_t *)array->data)[i] = (uint32_t)pts_number_to_int32(value);
    return;
  case PTS_FIELD_F32:
    ((float *)array->data)[i] = (float)value;
    return;
  default:
    ((double *)array->data)[i] = value;
  }
}

static inline double pts_typed_array_length(const pts_typed_array *array) { return (double)array->length; }
static inline double pts_typed_array_byte_offset(const pts_typed_array *array) { return (double)array->byte_offset; }
static inline double pts_typed_array_bytes_per_element(const pts_typed_array *array) { return (double)pts_field_kind_size(array->type->element); }
static inline double pts_typed_array_byte_length(const pts_typed_array *array) { return pts_typed_array_length(array) * pts_typed_array_bytes_per_element(array); }
static inline pts_arraybuffer *pts_typed_array_buffer(const pts_typed_array *array) { return array->buffer; }
static inline double pts_arraybuffer_byte_length(const pts_arraybuffer *buffer) { return (double)buffer->byte_length; }

/* new ArrayBuffer(length); RangeError for an invalid length. */
pts_arraybuffer *pts_arraybuffer_new(double length);
/* buffer.slice(start, end): a new buffer of a copy of those bytes (indices as Array's slice takes them). */
pts_arraybuffer *pts_arraybuffer_slice(const pts_arraybuffer *buffer, double start, double end);
/* ArrayBuffer.isView(value): whether value is a typed array. */
bool pts_arraybuffer_is_view(pts_value value);

/* new X(length), for X's descriptor type: zeros; RangeError for an invalid length. */
pts_typed_array *pts_typed_array_new(const pts_type *type, double length);
/* new X(array) and X.from(array): the elements of an array of any element kind, converted by ToNumber and then to X's elements. */
pts_typed_array *pts_typed_array_from_array(const pts_type *type, const pts_array *source);
/* new X(other) and X.from(other) for another typed array: its elements converted. */
pts_typed_array *pts_typed_array_from_typed(const pts_type *type, const pts_typed_array *source);
/* new X(buffer, byteOffset, length): a view of buffer, to its end if length isn't given; RangeError as JS throws it. */
pts_typed_array *pts_typed_array_on_buffer(const pts_type *type, pts_arraybuffer *buffer, double byte_offset, double length, bool length_given);

/* Reads and writes for any typed array, as pts_X_get and pts_X_set. */
double pts_typed_array_get(const pts_typed_array *array, double index);
double pts_typed_array_set(pts_typed_array *array, double index, double value);
/* array[index] as a dynamic value: undefined for an index the array doesn't have, as in JS. */
pts_value pts_typed_array_element(const pts_typed_array *array, double index);
/* array.at(index): from the end if index is negative; undefined out of range. */
pts_value pts_typed_array_at(const pts_typed_array *array, double index);

/*
 * The methods. Indices follow JS: negative ones count from the end, and the lowering
 * passes 0 or Infinity for one left out. subarray makes a view of the same buffer; slice,
 * with, toReversed and toSorted make new arrays; the others change array and return it.
 */
pts_typed_array *pts_typed_array_subarray(const pts_typed_array *array, double begin, double end);
pts_typed_array *pts_typed_array_slice(const pts_typed_array *array, double start, double end);
pts_typed_array *pts_typed_array_fill(pts_typed_array *array, double value, double start, double end);
pts_typed_array *pts_typed_array_copy_within(pts_typed_array *array, double target, double start, double end);
pts_typed_array *pts_typed_array_reverse(pts_typed_array *array);
pts_typed_array *pts_typed_array_to_reversed(const pts_typed_array *array);
/* sort(): numbers in ascending order, -0 before +0 and NaN last. */
pts_typed_array *pts_typed_array_sort(pts_typed_array *array);
pts_typed_array *pts_typed_array_to_sorted(const pts_typed_array *array);
/* with(index, value): a copy with value at index (from the end if negative); RangeError out of range. */
pts_typed_array *pts_typed_array_with(const pts_typed_array *array, double index, double value);
/* set(source, offset): source's elements written from offset on; RangeError if they don't fit. */
void pts_typed_array_set_array(pts_typed_array *array, const pts_array *source, double offset);
void pts_typed_array_set_typed(pts_typed_array *array, const pts_typed_array *source, double offset);
double pts_typed_array_index_of(const pts_typed_array *array, double value, double from);
/* lastIndexOf(value, from): a left-out from is Infinity. */
double pts_typed_array_last_index_of(const pts_typed_array *array, double value, double from);
bool pts_typed_array_includes(const pts_typed_array *array, double value, double from);
pts_string *pts_typed_array_join(const pts_typed_array *array, pts_string *separator);
pts_string *pts_typed_array_to_locale_string(const pts_typed_array *array);
/* The elements as a new number[]: what spreads, Array.from and iteration see. */
pts_array *pts_typed_array_to_array(const pts_typed_array *array);

/*
 * For each typed array X, for generated code: pts_X_get(array, index), which throws
 * RangeError for an index the array doesn't have; pts_X_set(array, index, value), which
 * converts value as JS does (modulo 2^n for the integers, clamped for Uint8ClampedArray,
 * rounded for Float32Array), ignores an index the array doesn't have, and returns value;
 * and X's constructors, pts_X_new, pts_X_from_array, pts_X_from_typed and pts_X_on_buffer.
 */
#define PTS_TYPED_ARRAY_ACCESS(name, ctype, element_kind, convert)                                                        \
  static inline double pts_##name##_get(const pts_typed_array *array, double index) {                                     \
    if (!pts_typed_array_has_index(array, index)) {                                                                       \
      pts_typed_array_index_error(array, index);                                                                          \
    }                                                                                                                     \
    return (double)((const ctype *)array->data)[(size_t)index];                                                           \
  }                                                                                                                       \
  static inline double pts_##name##_set(pts_typed_array *array, double index, double value) {                            \
    if (pts_typed_array_has_index(array, index)) {                                                                        \
      ((ctype *)array->data)[(size_t)index] = convert(value);                                                             \
    }                                                                                                                     \
    return value;                                                                                                         \
  }                                                                                                                       \
  static inline pts_typed_array *pts_##name##_new(double length) { return pts_typed_array_new(&pts_type_builtin_##name, length); } \
  static inline pts_typed_array *pts_##name##_from_array(const pts_array *source) {                                       \
    return pts_typed_array_from_array(&pts_type_builtin_##name, source);                                                  \
  }                                                                                                                       \
  static inline pts_typed_array *pts_##name##_from_typed(const pts_typed_array *source) {                                 \
    return pts_typed_array_from_typed(&pts_type_builtin_##name, source);                                                  \
  }                                                                                                                       \
  static inline pts_typed_array *pts_##name##_on_buffer(pts_arraybuffer *buffer, double byte_offset, double length, bool length_given) { \
    return pts_typed_array_on_buffer(&pts_type_builtin_##name, buffer, byte_offset, length, length_given);                \
  }
PTS_TYPED_ARRAYS(PTS_TYPED_ARRAY_ACCESS)
#undef PTS_TYPED_ARRAY_ACCESS

/* ---------------------------------------------------------------- regular expressions (D-132) */

/* A compiled regular expression: a JS pattern translated to PCRE2's syntax. Immutable, so threads share it. */
typedef struct pts_regex pts_regex;

/* JS's flags, as pts_regex_flags reports them. */
enum {
  PTS_REGEX_HAS_INDICES = 1u << 0, /* d */
  PTS_REGEX_GLOBAL = 1u << 1,      /* g */
  PTS_REGEX_IGNORE_CASE = 1u << 2, /* i */
  PTS_REGEX_MULTILINE = 1u << 3,   /* m */
  PTS_REGEX_DOT_ALL = 1u << 4,     /* s */
  PTS_REGEX_UNICODE = 1u << 5,     /* u */
  PTS_REGEX_STICKY = 1u << 6,      /* y */
};

/* Compiles a JS pattern with JS's flag letters; throws SyntaxError with V8's message for invalid flags or patterns. */
pts_regex *pts_regex_compile(const pts_string *pattern, const pts_string *flags);
unsigned pts_regex_flags(const pts_regex *regex);
/* The number of capturing groups. */
uint32_t pts_regex_group_count(const pts_regex *regex);
/* The name of capturing group group (1-based), or NULL if it has none. */
pts_string *pts_regex_group_name(const pts_regex *regex, uint32_t group);
/* pattern as RegExp's `source` shows it: `/` and line terminators escaped, "(?:)" for an empty one. */
pts_string *pts_regex_escape_source(const pts_string *pattern);

/* A UTF-16 index in a string and its byte offset: a loop over a string's matches carries it from one match to the next. */
typedef struct pts_regex_cursor {
  uint32_t unit, byte;
} pts_regex_cursor;

/*
 * Matches regex in subject at UTF-16 index from or, unless anchored, after it. On a match
 * it stores each group's start and end (group 0 is the whole match) as UTF-16 indices in
 * captures[2g] and captures[2g + 1], -1 for a group that took no part, and moves *cursor
 * (a known position at or before from; {0, 0} will do) to the match's end.
 */
bool pts_regex_exec(const pts_regex *regex, const pts_string *subject, uint32_t from, bool anchored, pts_regex_cursor *cursor,
                    int64_t *captures);

/* ---------------------------------------------------------------- pts:sync (D-128) */

/* 64-bit atomic cells in the first 8 bytes of a buffer; add and compare_exchange return the value before, as JS's Atomics do. */
int64_t pts_atomic_load(pts_arraybuffer *cell);
void pts_atomic_store(pts_arraybuffer *cell, int64_t value);
int64_t pts_atomic_add(pts_arraybuffer *cell, int64_t delta);
int64_t pts_atomic_compare_exchange(pts_arraybuffer *cell, int64_t expected, int64_t desired);

/* A lock in the first 4 bytes of a buffer (zero: free). Any thread may release it. */
void pts_sync_lock(pts_arraybuffer *lock);
bool pts_sync_try_lock(pts_arraybuffer *lock);
void pts_sync_unlock(pts_arraybuffer *lock);

/* ---------------------------------------------------------------- console */

/* console.log(args...): the arguments as Node's util.format formats them, on stdout (D-088). */
void pts_console_log(size_t argc, const pts_value *argv);
// console.log with spread arguments: each argv[i] with spread[i] is an array (boxed as an
// object) whose elements are arguments in its place (D-057).
void pts_console_log_spread(size_t argc, const pts_value *argv, const bool *spread);

/* The console methods that take any values (D-088); info, debug and dirxml are log, warn is error. */
typedef enum pts_console_method {
  PTS_CONSOLE_LOG,
  PTS_CONSOLE_ERROR,       /* error and warn: on stderr */
  PTS_CONSOLE_GROUP,       /* group and groupCollapsed: log the arguments, if any, then indent */
  PTS_CONSOLE_ASSERT,      /* assert(condition, ...data) */
  PTS_CONSOLE_TRACE,       /* trace(...data): "Trace: …" and the stack, on stderr */
  PTS_CONSOLE_COUNT,       /* count(label?) */
  PTS_CONSOLE_COUNT_RESET, /* countReset(label?) */
  PTS_CONSOLE_TIME,        /* time(label?) */
  PTS_CONSOLE_TIME_END,    /* timeEnd(label?) */
  PTS_CONSOLE_TIME_LOG,    /* timeLog(label?, ...data) */
} pts_console_method;

/* Calls a console method; spread is NULL, or says which arguments are spread as for pts_console_log_spread. */
void pts_console(pts_console_method method, size_t argc, const pts_value *argv, const bool *spread);
/* console.groupEnd(). */
void pts_console_group_end(void);
/* console.dir(value, { depth, showHidden }): depth is INFINITY for null. */
void pts_console_dir(pts_value value, double depth, bool show_hidden);
/* console.table(data, properties): properties is undefined or a string[]. */
void pts_console_table(pts_value data, pts_value properties);

/* ---------------------------------------------------------------- exceptions */

/*
 * Exceptions (D-068). Each `try` pushes a handler on its thread's chain: a jmp_buf in the
 * C frame of the function that runs it. `throw` pops the innermost handler, leaves the
 * exception in it and long-jumps there, so calls cost nothing and a try costs a _setjmp.
 * Generated C leaves a try block, normally or by a jump out of it, with pts_try_leave;
 * locals that a try block assigns and that code after the long jump may read are volatile.
 */
typedef struct pts_handler {
  struct pts_handler *prev;
  pts_value exception; /* what the throw that jumped here threw */
  /* A generator running above it (D-109): a throw that passes this marker finishes the generator; it has no jump then. */
  struct pts_iterator *generator;
  jmp_buf jump;
} pts_handler;

/* The innermost handler of this thread, or NULL. */
extern _Thread_local pts_handler *pts_handlers;

static inline void pts_try_enter(pts_handler *handler) {
  handler->prev = pts_handlers;
  handler->generator = NULL;
  pts_handlers = handler;
}

/* Leaves a try block: handler, and any inside it, are no longer current. */
static inline void pts_try_leave(pts_handler *handler) { pts_handlers = handler->prev; }

/* What the throw that jumped to handler threw (read from memory: the long jump left it there). */
static inline pts_value pts_caught(pts_handler *handler) { return *(volatile pts_value *)&handler->exception; }

/*
 * Throws exception to the innermost handler of this thread. With none, it is uncaught:
 * "Uncaught " and the error's stack, or the value as console.log shows it nested, go to
 * stderr and the process exits with code 1 (D-O07).
 */
_Noreturn void pts_throw(pts_value exception);

/*
 * Error and its native subclasses (D-068) are classes the runtime defines and the compiler
 * uses: an instance holds its name and message, its cause (PTS_VALUE_ABSENT unless the
 * options gave one), and its stack, formatted from the name and message when first read
 * (PTS_VALUE_ABSENT until then). The stack's member is named as the compiler names a
 * #private field, so that the classes programs derive from Error lay it out with the rest.
 * The native subclasses add nothing, so their instances are pts_errors too.
 */
typedef struct pts_error {
  const pts_type *pts_header;
  pts_string *name;
  pts_string *message;
  pts_value cause;
  pts_value pts_private_stack;
  void *pts_private_frames; /* the return addresses where it was made, for its stack (D-069) */
} pts_error;

/* AggregateError: an Error's members (laid out as pts_error's, as builtin classes are, D-068), then the errors it aggregates (D-113). */
typedef struct pts_aggregate_error {
  const pts_type *pts_header;
  pts_string *name;
  pts_string *message;
  pts_value cause;
  pts_value pts_private_stack;
  void *pts_private_frames;
  struct pts_array *errors; /* an any[] */
} pts_aggregate_error;
typedef pts_aggregate_error pts_class_builtin_AggregateError;

extern const pts_type pts_type_builtin_AggregateError;
/* new AggregateError(errors, message?, options?): errors is any iterable, copied into an any[]. */
void pts_aggregate_error_init(pts_aggregate_error *error, pts_value errors, pts_string *message, bool message_given, pts_object *options,
                              bool options_given);

/*
 * Node's errors of system calls (D-117): an Error's members, then the call's errno (a
 * negative number), code ("ENOENT"), syscall, and path and dest (PTS_VALUE_ABSENT if the
 * call had none). Their class is named "Error", as in Node.
 */
typedef struct pts_system_error {
  const pts_type *pts_header;
  pts_string *name;
  pts_string *message;
  pts_value cause;
  pts_value pts_private_stack;
  void *pts_private_frames;
  pts_value errno_value;
  pts_value code;
  pts_value syscall;
  pts_value path;
  pts_value dest;
} pts_system_error;

extern const pts_type pts_type_builtin_SystemError;
/* A system error with message and the call's details; error is libuv's (a negative errno). path and dest may be NULL. */
pts_value pts_system_error_new(int error, pts_string *message, const char *syscall, pts_string *path, pts_string *dest);
/* Node's uvException: the system error "CODE: description, syscall 'path' -> 'dest'". */
pts_value pts_uv_error_new(int error, const char *syscall, pts_string *path, pts_string *dest);
_Noreturn void pts_throw_uv_error(int error, const char *syscall, pts_string *path, pts_string *dest);

extern const pts_type pts_type_builtin_Error;
extern const pts_type pts_type_builtin_TypeError;
extern const pts_type pts_type_builtin_RangeError;
extern const pts_type pts_type_builtin_ReferenceError;
extern const pts_type pts_type_builtin_SyntaxError;
extern const pts_type pts_type_builtin_EvalError;
extern const pts_type pts_type_builtin_URIError;

/* The C types the compiler names the native error classes' instances by (pts_class_<class>). */
typedef pts_error pts_class_builtin_Error;
typedef pts_error pts_class_builtin_TypeError;
typedef pts_error pts_class_builtin_RangeError;
typedef pts_error pts_class_builtin_ReferenceError;
typedef pts_error pts_class_builtin_SyntaxError;
typedef pts_error pts_class_builtin_EvalError;
typedef pts_error pts_class_builtin_URIError;

/*
 * The JS name of each function of the program, by its C name (sorted), which a stack
 * trace shows (D-069): "" for a module's top-level code and anonymous functions. A
 * constructor of a class derived from Error is marked: the frames of the constructors
 * that made an error are left out of its stack, as V8 leaves them out.
 */
typedef struct pts_function_name {
  const char *c_name;
  const char *js_name;
  bool error_constructor;
} pts_function_name;
void pts_register_function_names(const pts_function_name *names, size_t count);

/* The constructor of every native error class: `new Error(message?, options?)`, whose options may give a cause. */
void pts_error_init(pts_error *error, pts_string *message, bool message_given, pts_object *options, bool options_given);
/* A new error of a native error class (or a class derived from one) with message. */
pts_error *pts_error_new(const pts_type *type, pts_string *message);
/* Whether object is an Error: an instance of Error or of a class derived from it. */
bool pts_is_error(const pts_object *object);
/* Error.prototype.toString: "name: message", or whichever of them isn't empty. */
pts_string *pts_error_to_string(pts_error *error);
/* The stack property: the name and message, then the frames where the error was made, formatted when first read. */
pts_value pts_error_get_stack(pts_error *error);
void pts_error_set_stack(pts_error *error, pts_value stack);

/* Throws a new error of type (a native error class) whose message is format's output. */
_Noreturn void pts_throw_new(const pts_type *type, const char *format, ...) __attribute__((format(printf, 2, 3)));

/* Throws a new error of the native error class name ("RangeError", say) with message. */
_Noreturn void pts_throw_error(pts_string *name, pts_string *message);

/* ---------------------------------------------------------------- fatal errors */

/*
 * Prints "Uncaught <error_name>: <message>" to stderr and exits with code 1 (D-O07):
 * for what a program can't catch, running out of memory and internal errors.
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

/*
 * The stack guard (D-069): the lowest address this thread's C stack may reach before a
 * call throws RangeError, with room left to throw it; 0 (no limit) until the thread sets it.
 */
extern _Thread_local uintptr_t pts_stack_limit;
/* Sets this thread's pts_stack_limit from its stack's bounds. */
void pts_stack_limit_init(void);
_Noreturn void pts_throw_stack_overflow(void);
/* The prologue of a generated function that may recurse: deep recursion throws RangeError rather than overflow the stack. */
#if defined(__x86_64__)
/*
 * A compare and a jump to pts_stack_overflow_stub (error.c), which aligns the stack and
 * calls pts_throw_stack_overflow: no call on the function's own path, so it needs no
 * frame and a leaf path (fib's base case) stays as short as it was.
 */
static inline __attribute__((always_inline)) void pts_stack_check(void) {
  __asm__ volatile("cmpq %%fs:pts_stack_limit@tpoff, %%rsp\n\tjb pts_stack_overflow_stub" ::: "cc");
}
#else
static inline void pts_stack_check(void) {
  if (__builtin_expect((uintptr_t)__builtin_frame_address(0) < pts_stack_limit, 0)) {
    pts_throw_stack_overflow();
  }
}
#endif

/* ---------------------------------------------------------------- lifecycle */

void pts_runtime_init(int argc, char **argv);

/* Flushes output and returns the process exit code: code, or if it is 0, process.exitCode's. */
int pts_runtime_exit(int code);

#ifdef __cplusplus
}
#endif

/* ---------------------------------------------------------------- generators */

/*
 * Generators (D-109). A generator object is a pts_iterator followed by its function's
 * frame, which holds every local of the function's body; step is the body compiled to a
 * state machine, which runs from the resume point state names until the next yield (it
 * returns false, value holding what it yielded and state where to resume) or until the
 * body returns (true, value holding what it returned). A resumed body reads mode and sent:
 * how it was resumed (next, throw, return) and with what.
 */
typedef enum pts_resume_mode { PTS_RESUME_NEXT = 0, PTS_RESUME_THROW = 1, PTS_RESUME_RETURN = 2 } pts_resume_mode;

enum {
  PTS_GENERATOR_START = 0,    /* not started: the body runs from the top */
  PTS_GENERATOR_RUNNING = -1, /* running: resuming it throws TypeError */
  PTS_GENERATOR_DONE = -2,    /* finished: it returned or threw */
};

typedef struct pts_iterator {
  const pts_type *type;
  bool (*step)(struct pts_iterator *it);
  int32_t state; /* a generator's resume point or PTS_GENERATOR_*; for a runtime iterator, its position */
  pts_resume_mode mode;
  pts_value sent;
  pts_value value;
  struct pts_promise *promise;   /* an async function's (D-112), which its body settles */
  struct pts_reaction *reaction; /* an async function's reaction to what it awaits, reused by each await */
  struct pts_request *requests;  /* an async generator's queue of next/return/throw calls (D-115) */
  bool awaiting;                 /* an async generator's body stopped at an await, not a yield */
  bool executing;                /* an async generator's body is running or awaiting */
} pts_iterator;

extern const pts_type pts_type_builtin_Generator;

/* A new generator object of size bytes (its frame included) that runs step, not started. */
pts_iterator *pts_generator_new(size_t size, bool (*step)(pts_iterator *));

/*
 * Resumes an iterator (a generator object) as next(sent), throw(sent) or return(sent)
 * would, and reports whether it is done; its value is then pts_iterator_value. JS's rules
 * for generators that haven't started, have finished or are running hold.
 */
bool pts_iterator_resume(pts_value iterator, pts_resume_mode mode, pts_value sent);
/*
 * An iterator over an iterable value: a generator object itself, what an object's
 * [Symbol.iterator]() (its method `@@iterator`) returns, as pts_iterator_of, or a runtime
 * iterator over what an array, string, map or set yields (an array's elements read live);
 * TypeError `text is not iterable` for anything else.
 */
pts_value pts_iterator_open(pts_value iterable, const pts_string *text);
/*
 * JS's iterable[Symbol.iterator](): a generator object or runtime iterator itself, what an
 * object's `@@iterator` method returns, or a new runtime iterator over an array's
 * elements, a string's code points, a map's entries or a set's elements (D-111).
 */
pts_value pts_iterator_get(pts_value iterable, const pts_string *text);
/* An iterator itself, or one that calls an object's next(), return() and throw() (D-111). */
pts_value pts_iterator_of(pts_value iterator);
/* Whether value is an object of the program's with a method `@@iterator` ([Symbol.iterator]). */
bool pts_value_has_iterator_method(pts_value value);
/* An iterator over a map's or set's values (0), keys (1) or [key, value] entries (2), as m.values(), m.keys() and m.entries() make: live, as JS's. */
pts_value pts_map_iterator_new(pts_value map, int32_t items);
/*
 * For console.log: whether object is a map's or set's iterator, and if so, an any[] of
 * the items it has yet to yield (without moving it) and whether they are entries.
 */
bool pts_map_iterator_remaining(const pts_object *object, struct pts_array **items, bool *entries);
/* What the iterator last yielded or returned. */
pts_value pts_iterator_value(pts_value iterator);
/* Closes an iterator a loop leaves early: return() without a value, its result unused. */
void pts_iterator_close(pts_value iterator);
/* A new any[] of the values an iterator yields until it is done. */
struct pts_array *pts_iterator_to_array(pts_value iterator);
/* A new any[] of the first count values an iterator yields, after which it is closed if it isn't done; with rest, of all of them. */
struct pts_array *pts_iterator_take(pts_value iterator, double count, bool rest);

/* ---------------------------------------------------------------- promises */

/*
 * Promises (D-112): ECMAScript's algorithms in V8's order of jobs, run from a queue of
 * microtasks per thread. An async function is a generator function's frame and step
 * function (D-109) whose yields are awaits: pts_async_start runs it until its first
 * await and returns its promise, and each settled await resumes it from a microtask.
 */
typedef struct pts_promise pts_promise;

extern const pts_type pts_type_builtin_Promise;

/* Runs an async function's body (it, a new frame) until it awaits or ends: its promise. */
pts_value pts_async_start(pts_iterator *it);
/* new Promise(executor): TypeError if executor isn't a function; what it throws rejects the promise. */
pts_value pts_promise_construct(pts_value executor, const pts_string *text);
/* promise.then(on_fulfilled, on_rejected), whose handlers may be undefined; a PromiseLike of the program's gets its own then() called. */
pts_value pts_promise_then(pts_value promise, pts_value on_fulfilled, pts_value on_rejected);
/* promise.finally(on_finally). */
pts_value pts_promise_finally(pts_value promise, pts_value on_finally);
/*
 * Promise.all, allSettled, any and race of an iterable (text: its source, for TypeError),
 * and Promise.withResolvers(): allSettled's results and withResolvers' object are plain
 * objects (hash-map objects of any); any rejects with an AggregateError.
 */
pts_value pts_promise_all(pts_value iterable, const pts_string *text);
pts_value pts_promise_all_settled(pts_value iterable, const pts_string *text);
pts_value pts_promise_any(pts_value iterable, const pts_string *text);
pts_value pts_promise_race(pts_value iterable, const pts_string *text);
pts_value pts_promise_with_resolvers(void);
/* A new AggregateError with the given errors (an array) and message. */
pts_value pts_aggregate_error_new(pts_value errors, pts_string *message);
/* x instanceof Promise. */
bool pts_value_is_promise(pts_value value);
/* Promise.resolve(value) and Promise.reject(reason). */
pts_value pts_promise_resolved(pts_value value);
pts_value pts_promise_rejected(pts_value reason);
/* A pending promise that the runtime settles (D-118): resolved with result if fulfilled, else rejected with it. */
pts_value pts_promise_pending(void);
void pts_promise_settle(pts_value promise, bool fulfilled, pts_value result);
/* For console.log: a promise's state (0 pending, 1 fulfilled, 2 rejected) and its value or reason. */
int32_t pts_promise_state(const pts_object *promise, pts_value *result);
/*
 * Runs the thread's microtasks until none is left; then a promise rejected without a
 * handler ends the program as an uncaught exception does.
 */
void pts_run_microtasks(void);
/* After the microtasks: the entry module's promise (top-level await) still pending ends the program with Node's exit code 13 (D-113). */
void pts_check_top_level(pts_value promise);
/*
 * Async iteration (D-115). An async generator is a generator's frame whose step function
 * stops at awaits (awaiting set) and yields; pts_async_generator_new makes its object, and
 * pts_async_iterator_resume is next(v), throw(v) or return(v) on it (a promise of an
 * iterator result), or on any async iterator. pts_async_iterator_open is
 * GetIterator(value, async): an async generator itself, what an object's
 * [Symbol.asyncIterator]() (`@@asyncIterator`) returns, or a sync iterator's items as
 * promises (CreateAsyncFromSyncIterator); pts_async_iterator_close is the promise of its
 * return() that a for await leaving early awaits (undefined if it has none).
 */
pts_value pts_async_generator_new(pts_iterator *it);
pts_value pts_async_iterator_open(pts_value iterable, const pts_string *text);
pts_value pts_async_iterator_resume(pts_value iterator, pts_resume_mode mode, pts_value sent);
pts_value pts_async_iterator_close(pts_value iterator);
/* An iterator result's done (TypeError if it isn't an object) and value. */
bool pts_iterator_result_done(pts_value result);
pts_value pts_iterator_result_value(pts_value result);

/* ---------------------------------------------------------------- the event loop */

/*
 * The event loops (D-114, D-121): a libuv loop per thread, the main thread's started at
 * the program's start (so that, as in Node, timers count from then), with Node's timers
 * and immediates; each callback is called through its dynamic entry, followed by the
 * microtasks it queued. The first `parallel` starts a loop per core (PTS_THREADS).
 */
typedef struct pts_timer pts_timer;
extern const pts_type pts_type_builtin_Timeout;

void pts_loop_init(void);
/* The thread's libuv loop (a uv_loop_t), started if it wasn't. */
struct uv_loop_s *pts_uv_loop(void);
/* Runs the main thread's loop until nothing referenced is left to wait for on any loop. */
void pts_run_loop(void);
/* `parallel e`: runs task, a closure of no parameters returning e's value, as a task on another loop; the promise of the value (its outcome if it is a promise). */
pts_value pts_parallel(pts_value task);
/* setTimeout (repeat false) and setInterval: a Timeout; ms as Node coerces it; args may be NULL. */
pts_value pts_timer_set(pts_value callback, pts_value ms, struct pts_array *args, bool repeat);
/* clearTimeout and clearInterval: nothing for anything but an active Timeout. */
void pts_timer_clear(pts_value timer);
/* timeout.ref() (referenced true) and unref(), which return the timeout; hasRef(); refresh(). */
pts_value pts_timer_ref(pts_value timer, bool referenced);
bool pts_timer_has_ref(pts_value timer);
pts_value pts_timer_refresh(pts_value timer);
/* setImmediate and clearImmediate. */
pts_value pts_immediate_set(pts_value callback, struct pts_array *args);
void pts_immediate_clear(pts_value immediate);
/* queueMicrotask(callback): a throw out of it is an uncaught exception. */
void pts_queue_microtask(pts_value callback);
/* performance.now(): ms since the program started. */
void pts_time_origin_init(void);
double pts_performance_now(void);

/* ---------------------------------------------------------------- the fork-join pool */

/*
 * The fork-join pool (D-122), after Rayon: worker threads with work-stealing deques,
 * apart from the loops; the thread that starts an operation helps until it is done.
 * With PTS_THREADS=1 there are no workers and everything runs on the caller (D-O06).
 */
/* A piece of work for pts_join: its argument, and whether it runs on another thread than the join's caller. */
typedef void (*pts_join_function)(void *argument, bool migrated);
/* Runs a(a_argument) and b(b_argument), perhaps at once on two threads, and returns when both are done; a throw out of either is rethrown then, a's first. */
void pts_join(pts_join_function a, void *a_argument, pts_join_function b, void *b_argument);
/*
 * An operation over the indices 0..count, split adaptively (Rayon's splitter) into
 * pieces of at least min_length indices (1 if 0) that the pool's threads run: leaf folds
 * the indices from..to into its result (result_size bytes, zeroed first), and combine,
 * if not NULL, folds the result of the piece to the right into the one to its left.
 */
typedef struct pts_par_op pts_par_op;
struct pts_par_op {
  void (*leaf)(const pts_par_op *op, size_t from, size_t to, void *result);
  void (*combine)(const pts_par_op *op, void *left, void *right);
  size_t result_size;
  size_t min_length;
};
/* Runs op over 0..count into result; a throw out of a piece is rethrown when all are done. */
void pts_par_run(const pts_par_op *op, size_t count, void *result);
/* The threads operations run on: the pool's workers and the caller. */
unsigned pts_pool_threads(void);
/*
 * The intrinsics of pts:parallel (D-123), whose closures are the library's: run leaf(from,
 * to) over pieces covering 0..count; the same with leaf returning a piece's value, folded
 * by combine(left, right) into the value of all; join of two closures of no parameters.
 */
void pts_parallel_for(struct pts_closure *leaf, double count);
/*
 * `parallel for` (D-127): pts_parallel_for that stops early once an iteration throws.
 * Each piece calls leaf on runs of at most PTS_PARALLEL_FOR_RUN indices and starts no
 * run after any run of the operation has thrown; the error thrown first is rethrown once
 * every piece is done.
 */
enum { PTS_PARALLEL_FOR_RUN = 256 };
void pts_parallel_for_each(struct pts_closure *leaf, double count);
pts_value pts_parallel_reduce(struct pts_closure *leaf, struct pts_closure *combine, double count);
void pts_parallel_join(struct pts_closure *a, struct pts_closure *b);
/* pts_pool_threads() as a number. */
double pts_parallel_threads(void);

/* Reports an uncaught exception on stderr and exits with code 1 (D-O07). */
_Noreturn void pts_report_uncaught(pts_value exception);
/* Called by a thread about to end the process (D-121): the first thread to call it goes on to end it; any other waits until it has. */
void pts_begin_exit(void);

/* ---------------------------------------------------------------- pts:process */

/*
 * The intrinsics of pts:process (D-116), which declares them by these names. argv is the
 * executable's path twice, then the program's arguments; environ lists the environment's
 * "NAME=value" entries.
 */
void pts_process_init(int argc, char **argv);
struct pts_array *pts_process_argv(void);
struct pts_array *pts_process_environ(void);
pts_string *pts_process_exec_path(void);
double pts_process_pid(void);
pts_string *pts_process_cwd(void);
/* process.exit(code): flushes output and exits at once. */
_Noreturn void pts_process_exit(double code);
/* process.exitCode = code: the code pts_runtime_exit returns for a program that ends normally. */
void pts_process_set_exit_code(double code);
/* process.stdout.write (fd 1) and process.stderr.write (fd 2), in order with console's output. */
void pts_process_write(double fd, pts_string *chunk);

/* ---------------------------------------------------------------- pts:fs */

/*
 * The intrinsics of pts:fs (D-117), over libuv's synchronous calls. Those that return
 * data throw Node's error; those that return a number return 0 or libuv's negative errno.
 */
pts_string *pts_fs_read_file(pts_string *path, double flags);
void pts_fs_write_file(pts_string *path, pts_string *data, double flags, double mode);
double pts_fs_access(pts_string *path, double mode);
/* stat or lstat (follow false) into stats: dev, mode, nlink, uid, gid, rdev, blksize, ino, size, blocks, then (seconds, nanoseconds) of atime, mtime, ctime, birthtime. */
double pts_fs_stat(pts_string *path, bool follow, struct pts_array *stats);
double pts_fs_mkdir(pts_string *path, double mode);
/* The sorted names in a directory; their UV_DIRENT_* types go to types. */
struct pts_array *pts_fs_scandir(pts_string *path, struct pts_array *types);
double pts_fs_unlink(pts_string *path);
double pts_fs_rmdir(pts_string *path);
double pts_fs_rename(pts_string *from, pts_string *to);
double pts_fs_copy_file(pts_string *source, pts_string *destination, double mode);
double pts_fs_symlink(pts_string *target, pts_string *path, double flags);
double pts_fs_chmod(pts_string *path, double mode);
pts_string *pts_fs_readlink(pts_string *path);
pts_string *pts_fs_mkdtemp(pts_string *prefix);
/* Node's error of a call that returned the errno error; path and dest may be empty. */
pts_error *pts_fs_error(double error, pts_string *syscall, pts_string *path, pts_string *dest);
pts_error *pts_fs_rm_directory_error(pts_string *path);
/*
 * The promise API's intrinsics (D-118): each runs its call on libuv's threadpool and
 * returns a promise its result settles, with Node's error for those that return data;
 * pts_fs_call_async ("access", "mkdir", "unlink", "rmdir", "rename", "copyfile",
 * "symlink", "chmod") and pts_fs_stat_async resolve with 0 or the negative errno.
 */
pts_value pts_fs_read_file_async(pts_string *path, double flags);
pts_value pts_fs_write_file_async(pts_string *path, pts_string *data, double flags, double mode);
pts_value pts_fs_call_async(pts_string *call, pts_string *path, pts_string *dest, double mode);
pts_value pts_fs_stat_async(pts_string *path, bool follow, struct pts_array *stats);
pts_value pts_fs_scandir_async(pts_string *path, struct pts_array *types);
pts_value pts_fs_readlink_async(pts_string *path);
pts_value pts_fs_mkdtemp_async(pts_string *prefix);
pts_value pts_fs_realpath_async(pts_string *path);

#endif /* PTS_H */
