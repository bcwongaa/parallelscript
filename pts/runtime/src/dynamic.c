/*
 * Operations on dynamic values (`any`, D-070): property reads and writes found at run
 * time through the descriptors, and the operators with JavaScript's conversions.
 */
#include "dynamic_internal.h"

#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>

/* ---------------------------------------------------------------- conversions */

static int64_t method_named(const pts_type *type, const char *name, pts_method_kind kind);

/*
 * Calls the program's toString or valueOf method of an object as OrdinaryToPrimitive does,
 * with no arguments (D-142): through its dynamic entry, which a program with such a method
 * has, so that the parameters it declares get undefined (or a TypeError if they can't take
 * it) and its result comes back as a dynamic value whatever its type. False if the
 * object's class has no such method.
 */
static bool call_conversion_method(const pts_object *object, const char *name, pts_value *result) {
  int64_t index = object->type->kind == PTS_KIND_OBJECT ? method_named(object->type, name, PTS_METHOD) : -1;
  if (index < 0 || object->type->methods[index].dynamic == NULL) {
    return false;
  }
  *result = (pts_value)object->type->methods[index].dynamic((pts_object *)object, 0, NULL);
  return true;
}

static bool is_primitive(pts_value value) {
  if (!pts_value_is_object(value)) {
    return true;
  }
  pts_kind kind = pts_value_as_object(value)->type->kind;
  return kind == PTS_KIND_STRING || kind == PTS_KIND_INT64 || kind == PTS_KIND_UINT64;
}

/*
 * OrdinaryToPrimitive of one of the program's objects (D-142): its toString then valueOf
 * for the string hint, valueOf then toString otherwise, the first whose result is a
 * primitive. Object.prototype's own toString gives "[object Object]" where the class has
 * none, and its valueOf the object, which isn't one; if neither gives a primitive, Node's
 * TypeError.
 */
static pts_value ordinary_to_primitive(const pts_object *object, bool string_hint) {
  const char *order[] = {string_hint ? "toString" : "valueOf", string_hint ? "valueOf" : "toString"};
  for (size_t i = 0; i < 2; i++) {
    pts_value result;
    if (call_conversion_method(object, order[i], &result)) {
      if (is_primitive(result)) {
        return result;
      }
    } else if (strcmp(order[i], "toString") == 0) {
      return pts_value_from_string(pts_string_from_cstr("[object Object]"));
    }
  }
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot convert object to primitive value");
}

pts_string *pts_program_object_to_string(const pts_object *object) {
  return pts_value_to_string(ordinary_to_primitive(object, true));
}

/*
 * ToPrimitive for the number and default hints: an object's valueOf() first, then
 * toString(), as OrdinaryToPrimitive does; a date's default is its string.
 */
pts_value pts_value_to_primitive(pts_value value, bool number_hint) {
  if (!pts_value_is_object(value)) {
    return value;
  }
  const pts_object *object = pts_value_as_object(value);
  switch (object->type->kind) {
  case PTS_KIND_STRING:
  case PTS_KIND_INT64:
  case PTS_KIND_UINT64:
    return value;
  case PTS_KIND_DATE: /* valueOf for a number, toString otherwise, as Date.prototype[Symbol.toPrimitive] does */
    if (number_hint) {
      return pts_value_from_number(((const pts_date *)object)->time);
    }
    return pts_value_from_string(pts_value_to_string(value));
  case PTS_KIND_OBJECT:
    return ordinary_to_primitive(object, false);
  default:
    return pts_value_from_string(pts_value_to_string(value));
  }
}

static bool is_string(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_STRING;
}

double pts_value_to_number(pts_value value) {
  value = pts_value_to_primitive(value, true);
  if (pts_value_is_number(value)) {
    return pts_value_as_number(value);
  }
  if (pts_value_is_object(value)) {
    const pts_object *object = pts_value_as_object(value);
    switch (object->type->kind) {
    case PTS_KIND_STRING:
      return pts_string_to_number((const pts_string *)object);
    case PTS_KIND_INT64:
      return (double)((const pts_boxed_int64 *)object)->value;
    case PTS_KIND_UINT64:
      return (double)((const pts_boxed_uint64 *)object)->value;
    default:
      break;
    }
  }
  switch (value) {
  case PTS_VALUE_NULL:
  case PTS_VALUE_FALSE:
    return 0;
  case PTS_VALUE_TRUE:
    return 1;
  default:
    return __builtin_nan(""); /* undefined */
  }
}

pts_value pts_value_add(pts_value left, pts_value right) {
  left = pts_value_to_primitive(left, false);
  right = pts_value_to_primitive(right, false);
  if (is_string(left) || is_string(right)) {
    return pts_value_from_string(pts_string_concat(pts_value_to_string(left), pts_value_to_string(right)));
  }
  return pts_value_from_number(pts_value_to_number(left) + pts_value_to_number(right));
}

/* JS's IsLessThan: strings by UTF-16 code units, everything else as numbers; undefined if either is NaN. */
static int compare(pts_value left, pts_value right, bool *undefined) {
  left = pts_value_to_primitive(left, true);
  right = pts_value_to_primitive(right, true);
  *undefined = false;
  if (is_string(left) && is_string(right)) {
    return pts_string_compare((const pts_string *)pts_value_as_object(left), (const pts_string *)pts_value_as_object(right));
  }
  double x = pts_value_to_number(left), y = pts_value_to_number(right);
  if (x != x || y != y) {
    *undefined = true;
    return 0;
  }
  return x < y ? -1 : x > y ? 1 : 0;
}

bool pts_value_less(pts_value left, pts_value right) {
  bool undefined;
  int order = compare(left, right, &undefined);
  return !undefined && order < 0;
}

bool pts_value_less_equal(pts_value left, pts_value right) {
  bool undefined;
  int order = compare(left, right, &undefined);
  return !undefined && order <= 0;
}

/* ---------------------------------------------------------------- properties */

enum { MAX_KEY = 256 };

/* The key as a C string, written into buffer (MAX_KEY bytes): a longer key is cut, which no field's name is. */
static const char *key_text(pts_value key, char *buffer) {
  pts_string *text = pts_value_to_string(key);
  size_t len = text->byte_len < MAX_KEY - 1 ? text->byte_len : MAX_KEY - 1;
  memcpy(buffer, text->bytes, len);
  buffer[len] = '\0';
  return buffer;
}

/* The index a key names, if it is an array index: a number or a canonical numeric string. */
static bool array_index(pts_value key, double *index) {
  if (pts_value_is_number(key)) {
    *index = pts_value_as_number(key);
    return true;
  }
  if (!is_string(key)) {
    return false;
  }
  double number = pts_string_to_number((const pts_string *)pts_value_as_object(key));
  if (number != number || number < 0 || (double)(uint64_t)number != number) {
    return false;
  }
  char canonical[PTS_NUMBER_BUF_SIZE];
  size_t len = pts_number_format(number, canonical);
  const pts_string *text = (const pts_string *)pts_value_as_object(key);
  if (len != text->byte_len || memcmp(canonical, text->bytes, len) != 0) {
    return false; /* "01" and "1.0" are keys, not indices */
  }
  *index = number;
  return true;
}

static bool key_is(pts_value key, const char *name) {
  if (!is_string(key)) {
    return false;
  }
  const pts_string *text = (const pts_string *)pts_value_as_object(key);
  return text->byte_len == strlen(name) && memcmp(text->bytes, name, text->byte_len) == 0;
}

static int64_t method_named(const pts_type *type, const char *name, pts_method_kind kind) {
  for (uint32_t i = 0; i < type->method_count; i++) {
    if (type->methods[i].kind == kind && strcmp(type->methods[i].name, name) == 0) {
      return i;
    }
  }
  return -1;
}

static const pts_field *field_named(const pts_type *type, const char *name) {
  for (uint32_t i = 0; i < type->field_count; i++) {
    if (!pts_field_is_accessor(type->fields[i].kind) && strcmp(type->fields[i].name, name) == 0) {
      return &type->fields[i];
    }
  }
  return NULL;
}

pts_value pts_call_getter(pts_function getter, pts_field_kind kind, pts_object *object) {
#define CALL(T) ((T(*)(pts_object *))getter)(object)
  switch (kind) {
  case PTS_FIELD_NUMBER: return pts_value_from_number(CALL(double));
  case PTS_FIELD_F32: return pts_value_from_number(CALL(float));
  case PTS_FIELD_BOOL: return pts_value_from_bool(CALL(bool));
  case PTS_FIELD_STRING:
  case PTS_FIELD_OBJECT: return pts_value_from_object(CALL(void *));
  case PTS_FIELD_I8: return pts_value_from_number(CALL(int8_t));
  case PTS_FIELD_I16: return pts_value_from_number(CALL(int16_t));
  case PTS_FIELD_I32: return pts_value_from_number(CALL(int32_t));
  case PTS_FIELD_I64: return pts_value_from_i64(CALL(int64_t));
  case PTS_FIELD_U8: return pts_value_from_number(CALL(uint8_t));
  case PTS_FIELD_U16: return pts_value_from_number(CALL(uint16_t));
  case PTS_FIELD_U32: return pts_value_from_number(CALL(uint32_t));
  case PTS_FIELD_U64: return pts_value_from_u64(CALL(uint64_t));
  default: return CALL(pts_value);
  }
#undef CALL
}

/* A method read through `any` (D-075): a closure that calls it on the object it was read from, through its dynamic entry. */
typedef struct bound_method {
  pts_closure closure;
  pts_object *self;
  const pts_method *method;
} bound_method;

static pts_value bound_method_call(pts_closure *closure, uint32_t argc, const pts_value *argv) {
  const bound_method *bound = (const bound_method *)closure;
  if (bound->method->dynamic == NULL) { /* a program that doesn't use `any` gives its methods no dynamic entries */
    pts_throw_new(&pts_type_builtin_TypeError, "Calling the method %s through `any` is not supported here", bound->method->name);
  }
  return bound->method->dynamic(bound->self, argc, argv);
}

/* The descriptors of bound methods, named as their methods: one per method table entry, made when first needed and kept. */
typedef struct bound_type {
  pts_type type;
  const pts_method *method;
  struct bound_type *next;
} bound_type;

static pthread_mutex_t bound_types_lock = PTHREAD_MUTEX_INITIALIZER;
static bound_type *bound_types;

static const pts_type *bound_method_type(const pts_method *method) {
  pthread_mutex_lock(&bound_types_lock);
  bound_type *found = bound_types;
  while (found != NULL && found->method != method) {
    found = found->next;
  }
  if (found == NULL) {
    found = malloc(sizeof *found); /* holds no collected pointers; the collector reads only its gc_descr */
    if (found == NULL) {
      pts_panic("RangeError", "Out of memory binding the method %s", method->name);
    }
    found->type = (pts_type){
        .kind = PTS_KIND_FUNCTION,
        .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(bound_method, self)),
        .name = method->name,
        .size = sizeof(bound_method),
        .call = bound_method_call,
    };
    found->method = method;
    found->next = bound_types;
    bound_types = found;
  }
  pthread_mutex_unlock(&bound_types_lock);
  return &found->type;
}

static pts_value bind_method(pts_object *object, const pts_method *method) {
  bound_method *bound = pts_object_new(bound_method_type(method));
  bound->self = object;
  bound->method = method;
  return pts_value_from_object(bound); /* no code: typed code gets it only through an adapter, its signature being NULL */
}

static pts_value object_get(pts_object *object, pts_value key) {
  const pts_type *type = object->type;
  char buffer[MAX_KEY];
  const char *name = key_text(key, buffer);
  const pts_field *field = field_named(type, name);
  if (field != NULL) {
    pts_value value = pts_value_present(pts_value_of_slot(field->kind, (const char *)object + field->offset));
    if (pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_FUNCTION) {
      int64_t method = method_named(type, name, PTS_METHOD);
      if (method >= 0) { /* an object literal's method: its closure takes the object first (D-044), so it is bound as a class's is */
        return bind_method(object, &type->methods[method]);
      }
    }
    return value;
  }
  int64_t getter = method_named(type, name, PTS_GETTER);
  if (getter >= 0) {
    return pts_call_getter(type->methods[getter].function, type->methods[getter].result, object);
  }
  int64_t method = method_named(type, name, PTS_METHOD);
  if (method >= 0) {
    return bind_method(object, &type->methods[method]);
  }
  return PTS_VALUE_UNDEFINED;
}

/* A typed array's element or property (D-125): its index, length, byteLength, byteOffset, buffer and BYTES_PER_ELEMENT. */
static pts_value typed_array_get(const pts_typed_array *array, pts_value key) {
  double index;
  if (array_index(key, &index)) {
    return pts_typed_array_element(array, index);
  }
  if (key_is(key, "length")) {
    return pts_value_from_number(pts_typed_array_length(array));
  }
  if (key_is(key, "byteLength")) {
    return pts_value_from_number(pts_typed_array_byte_length(array));
  }
  if (key_is(key, "byteOffset")) {
    return pts_value_from_number(pts_typed_array_byte_offset(array));
  }
  if (key_is(key, "BYTES_PER_ELEMENT")) {
    return pts_value_from_number(pts_typed_array_bytes_per_element(array));
  }
  return key_is(key, "buffer") ? pts_value_from_object(array->buffer) : PTS_VALUE_UNDEFINED;
}

/* The flags of a RegExp that its prototype's getters report (D-133). */
static const struct {
  const char *name;
  unsigned flag;
} regexp_flags[] = {
    {"hasIndices", PTS_REGEX_HAS_INDICES}, {"global", PTS_REGEX_GLOBAL}, {"ignoreCase", PTS_REGEX_IGNORE_CASE}, {"multiline", PTS_REGEX_MULTILINE},
    {"dotAll", PTS_REGEX_DOT_ALL},         {"unicode", PTS_REGEX_UNICODE}, {"sticky", PTS_REGEX_STICKY},
};

/* A RegExp's lastIndex, or a property its prototype's getters give: source, flags and each flag's. */
static pts_value regexp_get(const pts_regexp *regexp, pts_value key) {
  if (key_is(key, "lastIndex")) {
    return pts_value_from_number(regexp->lastIndex);
  }
  if (key_is(key, "source")) {
    return pts_value_from_string(pts_regexp_source(regexp));
  }
  if (key_is(key, "flags")) {
    return pts_value_from_string(pts_regexp_flags(regexp));
  }
  if (key_is(key, "unicodeSets")) {
    return PTS_VALUE_FALSE; /* the v flag isn't supported (D-132) */
  }
  for (size_t i = 0; i < sizeof regexp_flags / sizeof regexp_flags[0]; i++) {
    if (key_is(key, regexp_flags[i].name)) {
      return pts_value_from_bool(pts_regexp_has_flag(regexp, regexp_flags[i].flag));
    }
  }
  return PTS_VALUE_UNDEFINED;
}

/* A match array's index, input and groups (D-133), or undefined for another key. */
static pts_value match_array_get(const pts_array *array, pts_value key) {
  if (key_is(key, "index")) {
    return pts_match_index(array);
  }
  if (key_is(key, "input")) {
    return pts_match_input(array);
  }
  return key_is(key, "groups") ? pts_match_groups(array) : PTS_VALUE_UNDEFINED;
}

pts_value pts_dynamic_get(pts_value object, pts_value key) {
  if (pts_value_is_nullish(object)) {
    char buffer[MAX_KEY];
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot read properties of %s (reading '%s')", object == PTS_VALUE_NULL ? "null" : "undefined",
                  key_text(key, buffer));
  }
  if (!pts_value_is_object(object)) {
    return PTS_VALUE_UNDEFINED; /* a number's or boolean's methods are reached by calls (M3.3c) */
  }
  pts_object *target = pts_value_as_object(object);
  double index;
  switch (target->type->kind) {
  case PTS_KIND_STRING: {
    const pts_string *string = (const pts_string *)target;
    if (key_is(key, "length")) {
      return pts_value_from_number(string->utf16_len);
    }
    return array_index(key, &index) ? pts_string_element(string, index) : PTS_VALUE_UNDEFINED;
  }
  case PTS_KIND_ARRAY: {
    const pts_array *array = (const pts_array *)target;
    if (key_is(key, "length")) {
      return pts_value_from_number(pts_array_length(array));
    }
    if (array_index(key, &index)) {
      return pts_array_element(array, index);
    }
    return pts_is_match_array(target) ? match_array_get(array, key) : PTS_VALUE_UNDEFINED;
  }
  case PTS_KIND_TUPLE:
    if (key_is(key, "length")) {
      return pts_value_from_number(pts_tuple_length(target));
    }
    return array_index(key, &index) ? object_get(target, key) : PTS_VALUE_UNDEFINED;
  case PTS_KIND_TYPED_ARRAY:
    return typed_array_get((const pts_typed_array *)target, key);
  case PTS_KIND_ARRAY_BUFFER:
    return key_is(key, "byteLength") ? pts_value_from_number(pts_arraybuffer_byte_length((const pts_arraybuffer *)target)) : PTS_VALUE_UNDEFINED;
  case PTS_KIND_REGEXP:
    return regexp_get((const pts_regexp *)target, key);
  case PTS_KIND_RECORD: {
    pts_string *name = pts_value_to_string(key);
    return pts_map_get_value((const pts_map *)target, &name);
  }
  case PTS_KIND_MAP:
  case PTS_KIND_SET:
    return key_is(key, "size") ? pts_value_from_number(pts_map_size((const pts_map *)target)) : PTS_VALUE_UNDEFINED;
  case PTS_KIND_OBJECT:
    return object_get(target, key);
  case PTS_KIND_FUNCTION:
    if (key_is(key, "name")) {
      return pts_value_from_string(pts_value_function_name(object));
    }
    return key_is(key, "length") ? pts_value_from_number(pts_value_function_length(object)) : PTS_VALUE_UNDEFINED;
  default:
    return PTS_VALUE_UNDEFINED; /* a boxed integer's methods */
  }
}

/* Whether value is an integer that a slot of the given integer kind holds exactly. */
static bool fits_integer(pts_value value, pts_field_kind kind) {
  if (pts_value_is_object(value)) {
    pts_kind object_kind = pts_value_as_object(value)->type->kind;
    if (object_kind == PTS_KIND_INT64) {
      return kind == PTS_FIELD_I64 || (kind == PTS_FIELD_U64 && ((const pts_boxed_int64 *)pts_value_as_object(value))->value >= 0);
    }
    if (object_kind == PTS_KIND_UINT64) {
      return kind == PTS_FIELD_U64 || (kind == PTS_FIELD_I64 && ((const pts_boxed_uint64 *)pts_value_as_object(value))->value <= INT64_MAX);
    }
    return false;
  }
  if (!pts_value_is_number(value)) {
    return false;
  }
  double n = pts_value_as_number(value);
  if (n != __builtin_trunc(n)) {
    return false; /* NaN, infinities and fractions */
  }
  switch (kind) {
  case PTS_FIELD_I8: return n >= INT8_MIN && n <= INT8_MAX;
  case PTS_FIELD_I16: return n >= INT16_MIN && n <= INT16_MAX;
  case PTS_FIELD_I32: return n >= INT32_MIN && n <= INT32_MAX;
  case PTS_FIELD_I64: return n >= -9223372036854775808.0 && n < 9223372036854775808.0;
  case PTS_FIELD_U8: return n >= 0 && n <= UINT8_MAX;
  case PTS_FIELD_U16: return n >= 0 && n <= UINT16_MAX;
  case PTS_FIELD_U32: return n >= 0 && n <= UINT32_MAX;
  case PTS_FIELD_U64: return n >= 0 && n < 18446744073709551616.0;
  default: return false;
  }
}

const char *pts_value_description(pts_value value) {
  if (value == PTS_VALUE_NULL) {
    return "null";
  }
  if (pts_value_is_object(value)) {
    const pts_type *type = pts_value_as_object(value)->type;
    if (type->kind != PTS_KIND_STRING && type->kind != PTS_KIND_FUNCTION && type->name != NULL) {
      return type->name;
    }
  }
  return (const char *)pts_value_typeof(value)->bytes;
}

/* For a field, setter, element or hash-map entry; converting to an object type copies dynamic data. */
pts_value pts_storable(pts_field_kind kind, const pts_check *check, pts_value value, const char *name, const pts_type *type) {
  bool fits;
  switch (kind) {
  case PTS_FIELD_VALUE:
  case PTS_FIELD_OBJECT: {
    if (check == NULL && kind == PTS_FIELD_VALUE) {
      return value; /* any */
    }
    if (check == NULL) {
      pts_throw_new(&pts_type_builtin_TypeError, "Writing the object property %s of a %s through `any` is not supported", name, type->name);
    }
    pts_value converted;
    if (!pts_value_try_convert(value, check, &converted)) {
      pts_throw_new(&pts_type_builtin_TypeError, "Cannot set property %s of %s: expected %s, got %s", name, type->name, check->name,
                    pts_value_description(value));
    }
    return converted;
  }
  case PTS_FIELD_NUMBER:
  case PTS_FIELD_F32:
    fits = pts_value_is_number(value);
    break;
  case PTS_FIELD_BOOL:
    fits = value == PTS_VALUE_TRUE || value == PTS_VALUE_FALSE;
    break;
  case PTS_FIELD_STRING:
    fits = is_string(value);
    break;
  default:
    fits = fits_integer(value, kind);
  }
  if (!fits) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot set property %s of %s: expected %s, got %s", name, type->name,
                  pts_check_declared(kind, NULL)->name, pts_value_description(value));
  }
  return value;
}

void pts_call_setter(pts_function setter, pts_field_kind kind, pts_object *object, pts_value value) {
  union {
    double number;
    float f32;
    bool boolean;
    void *pointer;
    int8_t i8;
    int16_t i16;
    int32_t i32;
    int64_t i64;
    uint8_t u8;
    uint16_t u16;
    uint32_t u32;
    uint64_t u64;
  } argument = {0};
  pts_store_value_in_slot(kind, &argument, value);
#define SET(T, member) ((void (*)(pts_object *, T))setter)(object, argument.member); return
  switch (kind) { /* the argument in the setter's C type */
  case PTS_FIELD_NUMBER: SET(double, number);
  case PTS_FIELD_F32: SET(float, f32);
  case PTS_FIELD_BOOL: SET(bool, boolean);
  case PTS_FIELD_STRING:
  case PTS_FIELD_OBJECT: SET(void *, pointer);
  case PTS_FIELD_I8: SET(int8_t, i8);
  case PTS_FIELD_I16: SET(int16_t, i16);
  case PTS_FIELD_I32: SET(int32_t, i32);
  case PTS_FIELD_I64: SET(int64_t, i64);
  case PTS_FIELD_U8: SET(uint8_t, u8);
  case PTS_FIELD_U16: SET(uint16_t, u16);
  case PTS_FIELD_U32: SET(uint32_t, u32);
  case PTS_FIELD_U64: SET(uint64_t, u64);
  default: ((void (*)(pts_object *, pts_value))setter)(object, value); return;
  }
#undef SET
}

static void object_set(pts_object *object, pts_value key, pts_value value) {
  const pts_type *type = object->type;
  char buffer[MAX_KEY];
  const char *name = key_text(key, buffer);
  const pts_field *field = field_named(type, name);
  if (field != NULL) {
    pts_store_value_in_slot(field->kind, (char *)object + field->offset, pts_storable(field->kind, field->check, value, name, type));
    return;
  }
  int64_t setter = method_named(type, name, PTS_SETTER);
  if (setter >= 0) {
    pts_field_kind kind = type->methods[setter].result;
    pts_call_setter(type->methods[setter].function, kind, object, pts_storable(kind, type->methods[setter].check, value, name, type));
    return;
  }
  if (method_named(type, name, PTS_GETTER) >= 0) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot set property %s of #<%s> which has only a getter", name, type->name);
  }
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot add property %s, object is not extensible", name);
}

pts_value pts_dynamic_set(pts_value object, pts_value key, pts_value value) {
  char buffer[MAX_KEY];
  if (pts_value_is_nullish(object)) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot set properties of %s (setting '%s')", object == PTS_VALUE_NULL ? "null" : "undefined",
                  key_text(key, buffer));
  }
  if (!pts_value_is_object(object) || is_string(object)) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot create property '%s' on %s", key_text(key, buffer), pts_value_typeof(object)->bytes);
  }
  pts_object *target = pts_value_as_object(object);
  double index;
  switch (target->type->kind) {
  case PTS_KIND_ARRAY: {
    pts_array *array = (pts_array *)target;
    pts_field_kind kind = array->type->element;
    if (key_is(key, "length")) {
      pts_storable(PTS_FIELD_NUMBER, NULL, value, "length", array->type);
      pts_array_set_length(array, pts_value_as_number(value));
      return value;
    }
    if (!array_index(key, &index)) {
      pts_throw_new(&pts_type_builtin_TypeError, "Cannot add property %s, object is not extensible", key_text(key, buffer));
    }
    uint64_t element = 0;
    pts_store_value_in_slot(kind, &element, pts_storable(kind, array->type->check, value, key_text(key, buffer), array->type));
    pts_array_set(array, index, &element);
    return value;
  }
  case PTS_KIND_RECORD: {
    pts_map *record = (pts_map *)target;
    pts_value stored = pts_storable(record->type->element, record->type->check, value, key_text(key, buffer), record->type);
    pts_string *name = pts_value_to_string(key);
    uint64_t entry = 0;
    pts_store_value_in_slot(record->type->element, &entry, stored);
    pts_map_set(record, &name, &entry);
    return value;
  }
  case PTS_KIND_OBJECT:
  case PTS_KIND_TUPLE:
    object_set(target, key, value);
    return value;
  case PTS_KIND_TYPED_ARRAY:
    if (!array_index(key, &index)) {
      pts_throw_new(&pts_type_builtin_TypeError, "Cannot add property %s, object is not extensible", key_text(key, buffer));
    }
    pts_typed_array_set((pts_typed_array *)target, index, pts_value_to_number(value));
    return value;
  case PTS_KIND_REGEXP:
    if (key_is(key, "lastIndex")) {
      ((pts_regexp *)target)->lastIndex = pts_value_to_number(value);
      return value;
    }
    if (regexp_get((const pts_regexp *)target, key) != PTS_VALUE_UNDEFINED) {
      pts_throw_new(&pts_type_builtin_TypeError, "Cannot set property %s of [object Object] which has only a getter", key_text(key, buffer));
    }
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot create property '%s' on a %s", key_text(key, buffer), target->type->name);
  default:
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot create property '%s' on a %s", key_text(key, buffer), target->type->name);
  }
}

bool pts_dynamic_has(pts_value object, pts_value key) {
  char buffer[MAX_KEY];
  if (!pts_value_is_object(object) || is_string(object)) {
    char shown[MAX_KEY];
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot use 'in' operator to search for '%s' in %s", key_text(key, buffer),
                  key_text(object, shown));
  }
  pts_object *target = pts_value_as_object(object);
  double index;
  switch (target->type->kind) {
  case PTS_KIND_ARRAY:
    if (pts_is_match_array(target) && (key_is(key, "index") || key_is(key, "input") || key_is(key, "groups"))) {
      return true;
    }
    return key_is(key, "length") || (array_index(key, &index) && index < pts_array_length((const pts_array *)target));
  case PTS_KIND_TUPLE:
    return key_is(key, "length") || (array_index(key, &index) && index < pts_tuple_length(target));
  case PTS_KIND_REGEXP:
    return regexp_get((const pts_regexp *)target, key) != PTS_VALUE_UNDEFINED;
  case PTS_KIND_TYPED_ARRAY:
    return array_index(key, &index) && pts_typed_array_has_index((const pts_typed_array *)target, index);
  case PTS_KIND_RECORD: {
    pts_string *name = pts_value_to_string(key);
    return pts_map_has((const pts_map *)target, &name);
  }
  case PTS_KIND_OBJECT:
    return pts_object_has(target, key_text(key, buffer));
  default:
    return false;
  }
}

/* ---------------------------------------------------------------- calls */

static _Noreturn void not_a_function(const pts_string *text) {
  pts_throw_new(&pts_type_builtin_TypeError, "%.*s is not a function", (int)text->byte_len, text->bytes);
}

pts_value pts_dynamic_call_closure(pts_closure *function, uint32_t argc, const pts_value *argv) {
  if (function->type->call == NULL) { /* a program that doesn't use `any` gives its functions no dynamic entries */
    pts_throw_new(&pts_type_builtin_TypeError, "Calling a function through `any` is not supported here");
  }
  return function->type->call(function, argc, argv);
}

pts_value pts_dynamic_call(pts_value callee, const pts_string *text, uint32_t argc, const pts_value *argv) {
  if (!pts_value_is_object(callee) || pts_value_as_object(callee)->type->kind != PTS_KIND_FUNCTION) {
    not_a_function(text);
  }
  return pts_dynamic_call_closure((pts_closure *)pts_value_as_object(callee), argc, argv);
}

pts_value pts_adapter_call(pts_closure *self, uint32_t argc, const pts_value *argv) {
  return pts_dynamic_call_closure(((pts_adapter *)self)->function, argc, argv);
}

double pts_value_tuple_length(pts_value tuple) { return (double)pts_tuple_length(pts_value_as_object(tuple)); }

size_t pts_spread_length(pts_value value) {
  if (pts_value_is_object(value)) {
    const pts_object *object = pts_value_as_object(value);
    if (object->type->kind == PTS_KIND_ARRAY) {
      return pts_array_length_of((const pts_array *)object);
    }
    if (object->type->kind == PTS_KIND_TUPLE) {
      return pts_tuple_length(object);
    }
    if (object->type->kind == PTS_KIND_TYPED_ARRAY) {
      return ((const pts_typed_array *)object)->length;
    }
  }
  pts_throw_new(&pts_type_builtin_TypeError, "Cannot spread %s into the arguments of a call through `any`", pts_value_description(value));
}

pts_value pts_spread_element(pts_value value, size_t i) {
  const pts_object *object = pts_value_as_object(value);
  if (object->type->kind == PTS_KIND_ARRAY) {
    return pts_array_element((const pts_array *)object, (double)i);
  }
  if (object->type->kind == PTS_KIND_TYPED_ARRAY) {
    return pts_typed_array_element((const pts_typed_array *)object, (double)i);
  }
  const pts_field *field = &object->type->fields[i]; /* a tuple's fields are its elements, in order */
  return pts_value_of_slot(field->kind, (const char *)object + field->offset);
}

/* The arguments of a call with spread arguments, each spread one's elements in its place: a new array of count elements. */
static pts_value *spread_arguments(uint32_t argc, const pts_value *argv, const bool *spread, uint32_t *count) {
  size_t total = 0;
  for (uint32_t i = 0; i < argc; i++) {
    total += spread[i] ? pts_spread_length(argv[i]) : 1;
  }
  if (total > UINT32_MAX) {
    pts_throw_new(&pts_type_builtin_RangeError, "Maximum call stack size exceeded");
  }
  pts_value *values = GC_MALLOC(total * sizeof(pts_value) + 1);
  if (values == NULL) {
    pts_panic("RangeError", "Out of memory spreading %zu arguments", total);
  }
  size_t n = 0;
  for (uint32_t i = 0; i < argc; i++) {
    if (!spread[i]) {
      values[n++] = argv[i];
      continue;
    }
    for (size_t j = 0, length = pts_spread_length(argv[i]); j < length && n < total; j++) { /* an array may have shrunk */
      values[n++] = pts_spread_element(argv[i], j);
    }
  }
  *count = (uint32_t)n;
  return values;
}

pts_value pts_dynamic_call_spread(pts_value callee, const pts_string *text, uint32_t argc, const pts_value *argv, const bool *spread) {
  uint32_t count;
  pts_value *values = spread_arguments(argc, argv, spread, &count);
  return pts_dynamic_call(callee, text, count, values);
}

pts_value pts_dynamic_call_method_spread(pts_value object, pts_value key, const pts_string *text, uint32_t argc, const pts_value *argv, const bool *spread) {
  uint32_t count;
  pts_value *values = spread_arguments(argc, argv, spread, &count);
  return pts_dynamic_call_method(object, key, text, count, values);
}

pts_value pts_dynamic_call_method(pts_value object, pts_value key, const pts_string *text, uint32_t argc, const pts_value *argv) {
  char buffer[MAX_KEY];
  if (pts_value_is_nullish(object)) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot read properties of %s (reading '%s')", object == PTS_VALUE_NULL ? "null" : "undefined",
                  key_text(key, buffer));
  }
  if (!pts_value_is_object(object) || pts_value_as_object(object)->type->kind != PTS_KIND_OBJECT) {
    pts_value result;
    if (pts_builtin_method_call(object, key_text(key, buffer), argc, argv, &result)) {
      return result;
    }
    return pts_dynamic_call(pts_dynamic_get(object, key), text, argc, argv); /* a hash-map object's function, say */
  }
  pts_object *target = pts_value_as_object(object);
  int64_t method = method_named(target->type, key_text(key, buffer), PTS_METHOD);
  if (method >= 0 && target->type->methods[method].dynamic != NULL) {
    return target->type->methods[method].dynamic(target, argc, argv);
  }
  /* A property that holds a function: called without `this`, which only methods take. */
  return pts_dynamic_call(object_get(target, key), text, argc, argv);
}

/* ---------------------------------------------------------------- boundaries */

void pts_throw_type_mismatch(pts_value value, const char *expected) {
  pts_throw_new(&pts_type_builtin_TypeError, "Expected %s, got %s", expected, pts_value_description(value));
}

/* Whether value is an integer that the integer type of the given kind holds exactly: a boundary from `any` requires it. */
bool pts_value_is_integer_of(pts_value value, pts_field_kind kind) { return fits_integer(value, kind); }
