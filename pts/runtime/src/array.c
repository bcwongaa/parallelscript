/*
 * Arrays (D-038): growable, with the capacity in the buffer, safe against racing
 * threads in the sense of D-L13. Elements are copied with one store or load each
 * (element_store, element_load), never with memcpy, which may copy a word in pieces.
 */
#include "pts.h"

#include <inttypes.h>
#include <string.h>

enum { MIN_CAPACITY = 4 };

#define ARRAY_TYPE(element_kind)                                        \
  [element_kind] = {                                                    \
      .kind = PTS_KIND_ARRAY,                                           \
      .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_array, buffer)), \
      .name = "Array",                                                  \
      .size = sizeof(pts_array),                                        \
      .element = element_kind,                                          \
  }

static const pts_type array_types[] = {
    ARRAY_TYPE(PTS_FIELD_NUMBER), ARRAY_TYPE(PTS_FIELD_BOOL), ARRAY_TYPE(PTS_FIELD_STRING), ARRAY_TYPE(PTS_FIELD_VALUE),
    ARRAY_TYPE(PTS_FIELD_OBJECT), ARRAY_TYPE(PTS_FIELD_I8),   ARRAY_TYPE(PTS_FIELD_I16),    ARRAY_TYPE(PTS_FIELD_I32),
    ARRAY_TYPE(PTS_FIELD_I64),    ARRAY_TYPE(PTS_FIELD_U8),   ARRAY_TYPE(PTS_FIELD_U16),    ARRAY_TYPE(PTS_FIELD_U32),
    ARRAY_TYPE(PTS_FIELD_U64),    ARRAY_TYPE(PTS_FIELD_F32),
};

size_t pts_field_kind_size(pts_field_kind kind) {
  switch (kind) {
  case PTS_FIELD_BOOL:
  case PTS_FIELD_I8:
  case PTS_FIELD_U8:
    return 1;
  case PTS_FIELD_I16:
  case PTS_FIELD_U16:
    return 2;
  case PTS_FIELD_I32:
  case PTS_FIELD_U32:
  case PTS_FIELD_F32:
    return 4;
  default:
    return 8;
  }
}

static bool holds_pointers(pts_field_kind kind) {
  return kind == PTS_FIELD_STRING || kind == PTS_FIELD_VALUE || kind == PTS_FIELD_OBJECT;
}

/* element_store copies one element of size bytes from src to dst with a single store. */
static void element_store(void *dst, const void *src, size_t size) {
  switch (size) {
  case 1:
    __atomic_store_n((uint8_t *)dst, *(const uint8_t *)src, __ATOMIC_RELAXED);
    return;
  case 2:
    __atomic_store_n((uint16_t *)dst, *(const uint16_t *)src, __ATOMIC_RELAXED);
    return;
  case 4:
    __atomic_store_n((uint32_t *)dst, *(const uint32_t *)src, __ATOMIC_RELAXED);
    return;
  default:
    __atomic_store_n((uint64_t *)dst, *(const uint64_t *)src, __ATOMIC_RELAXED);
  }
}

/* element_load copies one element of size bytes from src to dst, reading it with a single load. */
static void element_load(void *dst, const void *src, size_t size) {
  switch (size) {
  case 1:
    *(uint8_t *)dst = __atomic_load_n((const uint8_t *)src, __ATOMIC_RELAXED);
    return;
  case 2:
    *(uint16_t *)dst = __atomic_load_n((const uint16_t *)src, __ATOMIC_RELAXED);
    return;
  case 4:
    *(uint32_t *)dst = __atomic_load_n((const uint32_t *)src, __ATOMIC_RELAXED);
    return;
  default:
    *(uint64_t *)dst = __atomic_load_n((const uint64_t *)src, __ATOMIC_RELAXED);
  }
}

/* new_buffer allocates a zeroed buffer for capacity elements of the given kind; only pointer elements are scanned. */
static pts_array_buffer *new_buffer(pts_field_kind kind, size_t capacity) {
  size_t bytes = sizeof(pts_array_buffer) + capacity * pts_field_kind_size(kind);
  pts_array_buffer *buffer = holds_pointers(kind) ? GC_MALLOC(bytes) : GC_MALLOC_ATOMIC(bytes);
  if (buffer == NULL) {
    pts_panic("RangeError", "Out of memory allocating an array of %zu elements", capacity);
  }
  if (!holds_pointers(kind)) {
    memset(buffer, 0, bytes); /* GC_MALLOC zeroes; the atomic allocator doesn't */
  }
  buffer->capacity = capacity;
  return buffer;
}

/* is_hole reports whether an element just read is a slot no racing push wrote: a zero pointer or dynamic value. */
static bool is_hole(pts_field_kind kind, const void *element) { return holds_pointers(kind) && *(const uint64_t *)element == 0; }

/* load_buffer and publish_buffer read and replace an array's buffer, publish_length its length. */
static pts_array_buffer *load_buffer(const pts_array *array) { return __atomic_load_n(&array->buffer, __ATOMIC_ACQUIRE); }

static void publish_buffer(pts_array *array, pts_array_buffer *buffer) { __atomic_store_n(&array->buffer, buffer, __ATOMIC_RELEASE); }

static void publish_length(pts_array *array, size_t length) { __atomic_store_n(&array->length, length, __ATOMIC_RELEASE); }

/*
 * with_room returns a buffer of array that holds its first length elements and has room
 * for needed elements in all: the current one, or a bigger copy, published first.
 */
static pts_array_buffer *with_room(pts_array *array, size_t length, size_t needed) {
  pts_array_buffer *buffer = load_buffer(array);
  if (needed <= buffer->capacity) {
    return buffer;
  }
  pts_field_kind kind = array->type->element;
  size_t capacity = buffer->capacity * 2 > needed ? buffer->capacity * 2 : needed;
  if (capacity < MIN_CAPACITY) {
    capacity = MIN_CAPACITY;
  }
  pts_array_buffer *bigger = new_buffer(kind, capacity);
  size_t kept = length < buffer->capacity ? length : buffer->capacity;
  memcpy(bigger->elements, buffer->elements, kept * pts_field_kind_size(kind)); /* nobody else sees bigger yet */
  publish_buffer(array, bigger);
  return bigger;
}

pts_array *pts_array_of(pts_field_kind kind, const void *elements, size_t count) {
  pts_array *array = pts_object_new(&array_types[kind]);
  array->buffer = new_buffer(kind, count);
  if (count > 0) {
    memcpy(array->buffer->elements, elements, count * pts_field_kind_size(kind));
  }
  array->length = count;
  return array;
}

void pts_array_index_error(const pts_array *array, double index) {
  char buf[PTS_NUMBER_BUF_SIZE];
  pts_number_format(index, buf);
  pts_panic("RangeError", "Array index %s is out of range (length %zu)", buf, pts_array_length_of(array));
}

pts_value pts_value_of_slot(pts_field_kind kind, const void *slot) {
  union {
    double number;
    float f32;
    bool boolean;
    pts_value value;
    const void *pointer;
    int8_t i8;
    int16_t i16;
    int32_t i32;
    int64_t i64;
    uint8_t u8;
    uint16_t u16;
    uint32_t u32;
    uint64_t u64;
  } element;
  element_load(&element, slot, pts_field_kind_size(kind));
  if (is_hole(kind, &element)) {
    return PTS_VALUE_UNDEFINED;
  }
  switch (kind) {
  case PTS_FIELD_NUMBER:
    return pts_value_from_number(element.number);
  case PTS_FIELD_F32:
    return pts_value_from_number(element.f32);
  case PTS_FIELD_BOOL:
    return pts_value_from_bool(element.boolean);
  case PTS_FIELD_VALUE:
    return element.value;
  case PTS_FIELD_STRING:
  case PTS_FIELD_OBJECT:
    return pts_value_from_object(element.pointer);
  case PTS_FIELD_I8:
    return pts_value_from_number(element.i8);
  case PTS_FIELD_I16:
    return pts_value_from_number(element.i16);
  case PTS_FIELD_I32:
    return pts_value_from_number(element.i32);
  case PTS_FIELD_I64:
    return pts_value_from_i64(element.i64);
  case PTS_FIELD_U8:
    return pts_value_from_number(element.u8);
  case PTS_FIELD_U16:
    return pts_value_from_number(element.u16);
  case PTS_FIELD_U32:
    return pts_value_from_number(element.u32);
  case PTS_FIELD_U64:
    return pts_value_from_u64(element.u64);
  }
  pts_panic("Error", "an array of unknown element kind %d", (int)kind);
}

void pts_store_value_in_slot(pts_field_kind kind, void *slot, pts_value value) {
  union {
    double number;
    float f32;
    bool boolean;
    pts_value value;
    const void *pointer;
    int8_t i8;
    int16_t i16;
    int32_t i32;
    int64_t i64;
    uint8_t u8;
    uint16_t u16;
    uint32_t u32;
    uint64_t u64;
  } element = {0};
  switch (kind) {
  case PTS_FIELD_NUMBER:
    element.number = pts_value_as_number(value);
    break;
  case PTS_FIELD_F32:
    element.f32 = (float)pts_value_as_number(value);
    break;
  case PTS_FIELD_BOOL:
    element.boolean = value == PTS_VALUE_TRUE;
    break;
  case PTS_FIELD_VALUE:
    element.value = value;
    break;
  case PTS_FIELD_STRING:
  case PTS_FIELD_OBJECT:
    element.pointer = pts_value_as_object(value);
    break;
  case PTS_FIELD_I8:
    element.i8 = (int8_t)pts_value_as_number(value);
    break;
  case PTS_FIELD_I16:
    element.i16 = (int16_t)pts_value_as_number(value);
    break;
  case PTS_FIELD_I32:
    element.i32 = (int32_t)pts_value_as_number(value);
    break;
  case PTS_FIELD_I64:
    element.i64 = pts_value_as_i64(value);
    break;
  case PTS_FIELD_U8:
    element.u8 = (uint8_t)pts_value_as_number(value);
    break;
  case PTS_FIELD_U16:
    element.u16 = (uint16_t)pts_value_as_number(value);
    break;
  case PTS_FIELD_U32:
    element.u32 = (uint32_t)pts_value_as_number(value);
    break;
  case PTS_FIELD_U64:
    element.u64 = pts_value_as_u64(value);
    break;
  default:
    pts_panic("Error", "a slot of unknown kind %d", (int)kind);
  }
  element_store(slot, &element, pts_field_kind_size(kind));
}

pts_value pts_array_element(const pts_array *array, double index) {
  size_t size = pts_field_kind_size(array->type->element);
  const void *slot = pts_array_slot_or_null(array, index, size);
  return slot == NULL ? PTS_VALUE_UNDEFINED : pts_value_of_slot(array->type->element, slot);
}

void pts_array_set(pts_array *array, double index, const void *value) {
  size_t size = pts_field_kind_size(array->type->element);
  void *slot = pts_array_slot_or_null(array, index, size);
  if (slot != NULL) {
    element_store(slot, value, size);
    return;
  }
  size_t length = pts_array_length_of(array);
  if (index != (double)length) {
    pts_array_index_error(array, index); /* JS would leave holes before it, which arrays here can't have */
  }
  pts_array_push(array, value, 1);
}

double pts_array_push(pts_array *array, const void *values, size_t count) {
  size_t size = pts_field_kind_size(array->type->element);
  size_t length = pts_array_length_of(array);
  pts_array_buffer *buffer = with_room(array, length, length + count);
  for (size_t i = 0; i < count; i++) {
    element_store(buffer->elements + (length + i) * size, (const unsigned char *)values + i * size, size);
  }
  publish_length(array, length + count);
  return (double)(length + count);
}

double pts_array_push_all(pts_array *array, const pts_array *values) {
  return pts_array_push(array, load_buffer(values)->elements, pts_array_length_of(values));
}

double pts_array_unshift_all(pts_array *array, const pts_array *values) {
  return pts_array_unshift(array, load_buffer(values)->elements, pts_array_length_of(values));
}

double pts_array_unshift(pts_array *array, const void *values, size_t count) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  size_t length = pts_array_length_of(array);
  pts_array_buffer *old = load_buffer(array);
  size_t kept = length < old->capacity ? length : old->capacity;
  /* A new buffer, filled before it is published, so that readers never see elements half moved. */
  size_t capacity = kept + count > old->capacity ? kept + count : old->capacity;
  pts_array_buffer *buffer = new_buffer(kind, capacity);
  memcpy(buffer->elements, values, count * size);
  memcpy(buffer->elements + count * size, old->elements, kept * size);
  publish_buffer(array, buffer);
  publish_length(array, kept + count);
  return (double)(kept + count);
}

void pts_array_pop(pts_array *array, void *out) {
  size_t length = pts_array_length_of(array);
  if (length == 0) {
    pts_panic("RangeError", "pop() of an empty array");
  }
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  element_load(out, pts_array_slot(array, (double)(length - 1), size), size);
  if (is_hole(kind, out)) {
    pts_array_index_error(array, (double)(length - 1));
  }
  publish_length(array, length - 1); /* the slot keeps its element, which racing readers may still read */
}

void pts_array_shift(pts_array *array, void *out) {
  size_t length = pts_array_length_of(array);
  if (length == 0) {
    pts_panic("RangeError", "shift() of an empty array");
  }
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *buffer = load_buffer(array);
  if (length > buffer->capacity) {
    length = buffer->capacity; /* the length of a buffer published later; this one holds less */
  }
  element_load(out, buffer->elements, size);
  if (is_hole(kind, out)) {
    pts_array_index_error(array, 0);
  }
  for (size_t i = 1; i < length; i++) {
    uint64_t element;
    element_load(&element, buffer->elements + i * size, size);
    element_store(buffer->elements + (i - 1) * size, &element, size);
  }
  publish_length(array, length - 1);
}

pts_value pts_array_pop_value(pts_array *array) {
  if (pts_array_length_of(array) == 0) {
    return PTS_VALUE_UNDEFINED;
  }
  uint64_t element;
  pts_array_pop(array, &element);
  return pts_value_of_slot(array->type->element, &element);
}

pts_value pts_array_shift_value(pts_array *array) {
  if (pts_array_length_of(array) == 0) {
    return PTS_VALUE_UNDEFINED;
  }
  uint64_t element;
  pts_array_shift(array, &element);
  return pts_value_of_slot(array->type->element, &element);
}

double pts_array_set_length(pts_array *array, double length) {
  size_t current = pts_array_length_of(array);
  if (!(length >= 0 && length <= 4294967295.0) || (double)(size_t)length != length) {
    pts_panic("RangeError", "Invalid array length");
  }
  if ((size_t)length > current) {
    char buf[PTS_NUMBER_BUF_SIZE];
    pts_number_format(length, buf);
    pts_panic("RangeError", "Invalid array length %s: setting length can only shrink an array (length %zu)", buf, current);
  }
  publish_length(array, (size_t)length);
  return length;
}

/* ---------------------------------------------------------------- methods (M2.4b) */

/* JS's ToIntegerOrInfinity of a relative index, clamped to [0, length]: negative ones count from the end. */
static size_t relative_index(double position, size_t length) {
  if (position != position) {
    return 0; /* NaN */
  }
  double integer = __builtin_trunc(position);
  if (integer < 0) {
    integer += (double)length;
    return integer < 0 ? 0 : (size_t)integer;
  }
  return integer > (double)length ? length : (size_t)integer;
}

/* readable_length is the length clamped to the capacity of buffer, which a racing push may not have reached. */
static size_t readable_length(const pts_array *array, const pts_array_buffer *buffer) {
  size_t length = pts_array_length_of(array);
  return length < buffer->capacity ? length : buffer->capacity;
}

/* copy_elements copies count elements from a buffer others may write (one load each) to one only we see. */
static void copy_elements(unsigned char *to, const unsigned char *from, size_t count, size_t size) {
  for (size_t i = 0; i < count; i++) {
    element_load(to + i * size, from + i * size, size);
  }
}

/* new_array makes an array of the given kind holding count elements read from from. */
static pts_array *new_array(pts_field_kind kind, const unsigned char *from, size_t count) {
  pts_array *array = pts_object_new(&array_types[kind]);
  array->buffer = new_buffer(kind, count);
  copy_elements(array->buffer->elements, from, count, pts_field_kind_size(kind));
  array->length = count;
  return array;
}

pts_array *pts_array_slice(const pts_array *array, double start, double end) {
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  size_t from = relative_index(start, length), to = relative_index(end, length);
  size_t size = pts_field_kind_size(array->type->element);
  return new_array(array->type->element, buffer->elements + from * size, to > from ? to - from : 0);
}

pts_array *pts_array_copy(const pts_array *array) { return pts_array_slice(array, 0, (double)SIZE_MAX); }

pts_array *pts_array_splice(pts_array *array, double start, double delete_count, const void *items, size_t count) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *old = load_buffer(array);
  size_t length = readable_length(array, old);
  size_t from = relative_index(start, length);
  double wanted = delete_count != delete_count ? 0 : __builtin_trunc(delete_count);
  size_t deleted = wanted <= 0 ? 0 : wanted >= (double)(length - from) ? length - from : (size_t)wanted;
  pts_array *removed = new_array(kind, old->elements + from * size, deleted);
  /* A new buffer, filled before it is published, as unshift does. */
  size_t new_length = length - deleted + count;
  pts_array_buffer *buffer = new_buffer(kind, new_length > old->capacity ? new_length : old->capacity);
  copy_elements(buffer->elements, old->elements, from, size);
  if (count > 0) {
    memcpy(buffer->elements + from * size, items, count * size);
  }
  copy_elements(buffer->elements + (from + count) * size, old->elements + (from + deleted) * size, length - from - deleted, size);
  publish_buffer(array, buffer);
  publish_length(array, new_length);
  return removed;
}

void pts_array_append(pts_array *array, const pts_array *other) {
  size_t size = pts_field_kind_size(array->type->element);
  pts_array_buffer *source = load_buffer(other);
  size_t count = readable_length(other, source);
  size_t length = pts_array_length_of(array);
  pts_array_buffer *buffer = with_room(array, length, length + count);
  for (size_t i = 0; i < count; i++) {
    uint64_t element = 0;
    element_load(&element, source->elements + i * size, size);
    element_store(buffer->elements + (length + i) * size, &element, size);
  }
  publish_length(array, length + count);
}

/* same_value reports whether two elements of a kind are equal: by === or, if zero is true, by SameValueZero (NaN is NaN). */
static bool same_value(pts_field_kind kind, const void *a, const void *b, bool zero) {
  switch (kind) {
  case PTS_FIELD_NUMBER: {
    double x = *(const double *)a, y = *(const double *)b;
    return x == y || (zero && x != x && y != y);
  }
  case PTS_FIELD_F32: {
    float x = *(const float *)a, y = *(const float *)b;
    return x == y || (zero && x != x && y != y);
  }
  case PTS_FIELD_STRING:
    return *(pts_string *const *)a != NULL && *(pts_string *const *)b != NULL && pts_string_equals(*(pts_string *const *)a, *(pts_string *const *)b);
  case PTS_FIELD_VALUE: {
    pts_value x = *(const pts_value *)a, y = *(const pts_value *)b;
    if (pts_value_is_number(x) && pts_value_is_number(y)) {
      double dx = pts_value_as_number(x), dy = pts_value_as_number(y);
      return dx == dy || (zero && dx != dx && dy != dy);
    }
    if (x != 0 && y != 0 && pts_value_is_object(x) && pts_value_is_object(y) && pts_value_as_object(x)->type->kind == PTS_KIND_STRING &&
        pts_value_as_object(y)->type->kind == PTS_KIND_STRING) {
      return pts_string_equals((const pts_string *)pts_value_as_object(x), (const pts_string *)pts_value_as_object(y));
    }
    return x == y;
  }
  default:
    return memcmp(a, b, pts_field_kind_size(kind)) == 0; /* booleans, integers and object references */
  }
}

/* find returns the index of the first (or, backwards, last) element same as value from from on, or -1. */
static double find(const pts_array *array, const void *value, size_t from, bool backwards, bool zero) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  for (size_t i = from; i < length; backwards ? i-- : i++) {
    uint64_t element = 0;
    element_load(&element, buffer->elements + i * size, size);
    if (!is_hole(kind, &element) && same_value(kind, &element, value, zero)) {
      return (double)i;
    }
  }
  return -1;
}

double pts_array_index_of(const pts_array *array, const void *value, double from) {
  return find(array, value, relative_index(from, pts_array_length_of(array)), false, false);
}

double pts_array_last_index_of(const pts_array *array, const void *value, double from) {
  size_t length = pts_array_length_of(array);
  if (length == 0) {
    return -1;
  }
  double integer = from != from ? 0 : __builtin_trunc(from);
  if (integer < 0) {
    integer += (double)length;
    if (integer < 0) {
      return -1;
    }
  }
  return find(array, value, integer >= (double)length ? length - 1 : (size_t)integer, true, false);
}

bool pts_array_includes(const pts_array *array, const void *value, double from) {
  return find(array, value, relative_index(from, pts_array_length_of(array)), false, true) >= 0;
}

/* The arrays being joined on this thread, so that a cycle joins as "" as in JS. */
static __thread const pts_array *joining[64];
static __thread size_t joining_depth;

pts_string *pts_object_to_string(const pts_object *object) {
  if (object->type->kind == PTS_KIND_ARRAY) {
    return pts_array_join((const pts_array *)object, NULL);
  }
  for (uint32_t i = 0; i < object->type->method_count; i++) {
    const pts_method *method = &object->type->methods[i];
    if (method->kind == PTS_METHOD && strcmp(method->name, "toString") == 0) {
      return ((pts_string * (*)(pts_object *)) method->function)((pts_object *)object);
    }
  }
  return pts_string_from_cstr("[object Object]");
}

/* element_string is the string join and sort use for the element at slot: "" for undefined, null and holes. */
static pts_string *element_string(pts_field_kind kind, const void *slot) {
  uint64_t element = 0;
  element_load(&element, slot, pts_field_kind_size(kind));
  if (is_hole(kind, &element)) {
    return pts_string_from_cstr("");
  }
  switch (kind) {
  case PTS_FIELD_STRING:
    return *(pts_string **)&element;
  case PTS_FIELD_OBJECT:
    return pts_object_to_string(*(const pts_object **)&element);
  case PTS_FIELD_VALUE: {
    pts_value value = *(pts_value *)&element;
    if (value == PTS_VALUE_UNDEFINED || value == PTS_VALUE_NULL) {
      return pts_string_from_cstr("");
    }
    if (pts_value_is_object(value) && pts_value_as_object(value)->type->kind != PTS_KIND_STRING) {
      return pts_object_to_string(pts_value_as_object(value));
    }
    return pts_value_to_string(value);
  }
  default:
    return pts_value_to_string(pts_value_of_slot(kind, &element));
  }
}

/* joined concatenates the strings of elements [from, to) with separators, halving the range so copying stays O(n log n). */
static pts_string *joined(const pts_array *array, const pts_array_buffer *buffer, size_t from, size_t to, pts_string *separator) {
  pts_field_kind kind = array->type->element;
  if (to - from == 1) {
    return element_string(kind, buffer->elements + from * pts_field_kind_size(kind));
  }
  size_t middle = from + (to - from) / 2;
  pts_string *left = joined(array, buffer, from, middle, separator);
  return pts_string_concat(pts_string_concat(left, separator), joined(array, buffer, middle, to, separator));
}

pts_string *pts_array_join(const pts_array *array, pts_string *separator) {
  if (separator == NULL) {
    separator = pts_string_from_cstr(",");
  }
  for (size_t i = 0; i < joining_depth && i < 64; i++) {
    if (joining[i] == array) {
      return pts_string_from_cstr("");
    }
  }
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  if (length == 0) {
    return pts_string_from_cstr("");
  }
  if (joining_depth < 64) {
    joining[joining_depth] = array;
  }
  joining_depth++;
  pts_string *result = joined(array, buffer, 0, length, separator);
  joining_depth--;
  return result;
}

pts_array *pts_array_reverse(pts_array *array) {
  size_t size = pts_field_kind_size(array->type->element);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  for (size_t i = 0; i < length / 2; i++) {
    uint64_t low = 0, high = 0;
    element_load(&low, buffer->elements + i * size, size);
    element_load(&high, buffer->elements + (length - 1 - i) * size, size);
    element_store(buffer->elements + i * size, &high, size);
    element_store(buffer->elements + (length - 1 - i) * size, &low, size);
  }
  return array;
}

/* A sort entry: an element and the string it sorts by. */
typedef struct sort_entry {
  pts_string *key;
  uint64_t element;
} sort_entry;

/* merge_sort sorts entries[0, count) stably by key in UTF-16 order, using scratch. */
static void merge_sort(sort_entry *entries, sort_entry *scratch, size_t count) {
  if (count < 2) {
    return;
  }
  size_t half = count / 2;
  merge_sort(entries, scratch, half);
  merge_sort(entries + half, scratch, count - half);
  size_t left = 0, right = half, out = 0;
  while (left < half && right < count) {
    scratch[out++] = pts_string_compare(entries[right].key, entries[left].key) < 0 ? entries[right++] : entries[left++];
  }
  while (left < half) {
    scratch[out++] = entries[left++];
  }
  while (right < count) {
    scratch[out++] = entries[right++];
  }
  memcpy(entries, scratch, count * sizeof *entries);
}

pts_array *pts_array_sort(pts_array *array) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  /* Scanned memory, since the keys are strings the collector must keep. */
  sort_entry *entries = GC_MALLOC(2 * length * sizeof *entries + 1);
  if (entries == NULL) {
    pts_panic("RangeError", "Out of memory sorting an array of %zu elements", length);
  }
  for (size_t i = 0; i < length; i++) {
    element_load(&entries[i].element, buffer->elements + i * size, size);
    entries[i].key = element_string(kind, buffer->elements + i * size);
  }
  merge_sort(entries, entries + length, length);
  for (size_t i = 0; i < length; i++) {
    element_store(buffer->elements + i * size, &entries[i].element, size);
  }
  return array;
}

/* compare_sort sorts elements[0, count) stably by compare's results (called through call), using scratch. */
static void compare_sort(uint64_t *elements, uint64_t *scratch, size_t count, pts_closure *compare,
                         double (*call)(pts_closure *, const void *, const void *)) {
  if (count < 2) {
    return;
  }
  size_t half = count / 2;
  compare_sort(elements, scratch, half, compare, call);
  compare_sort(elements + half, scratch, count - half, compare, call);
  size_t left = 0, right = half, out = 0;
  while (left < half && right < count) {
    scratch[out++] = call(compare, &elements[left], &elements[right]) > 0 ? elements[right++] : elements[left++]; /* NaN keeps the order */
  }
  while (left < half) {
    scratch[out++] = elements[left++];
  }
  while (right < count) {
    scratch[out++] = elements[right++];
  }
  memcpy(elements, scratch, count * sizeof *elements);
}

pts_array *pts_array_sort_with(pts_array *array, pts_closure *compare, double (*call)(pts_closure *, const void *, const void *)) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  /* Scanned memory: the elements may be pointers the collector must keep. Holes (D-038) go last, as JS sorts undefined. */
  uint64_t *elements = GC_MALLOC(2 * length * sizeof *elements + 1);
  if (elements == NULL) {
    pts_panic("RangeError", "Out of memory sorting an array of %zu elements", length);
  }
  size_t count = 0;
  for (size_t i = 0; i < length; i++) {
    if (!is_hole(kind, buffer->elements + i * size)) {
      element_load(&elements[count++], buffer->elements + i * size, size);
    }
  }
  compare_sort(elements, elements + length, count, compare, call);
  /* The comparator may have changed the array: write to its current buffer, as far as it reaches. */
  buffer = load_buffer(array);
  size_t now = readable_length(array, buffer);
  for (size_t i = 0; i < length && i < now; i++) {
    uint64_t hole = 0;
    element_store(buffer->elements + i * size, i < count ? &elements[i] : &hole, size);
  }
  return array;
}

/* flatten_into appends the elements of array to result, those that are arrays flattened depth levels more. */
static void flatten_into(pts_array *result, const pts_array *array, double depth) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  for (size_t i = 0; i < length; i++) {
    uint64_t element = 0;
    element_load(&element, buffer->elements + i * size, size);
    if (is_hole(kind, &element)) {
      continue; /* JS's flat skips holes too */
    }
    if (depth >= 1 && kind == PTS_FIELD_OBJECT && (*(const pts_object **)&element)->type->kind == PTS_KIND_ARRAY) {
      flatten_into(result, *(const pts_array **)&element, depth - 1);
    } else {
      pts_array_push(result, &element, 1);
    }
  }
}

pts_array *pts_array_flat(const pts_array *array, double depth, pts_field_kind result_kind) {
  pts_array *result = pts_array_of(result_kind, NULL, 0);
  flatten_into(result, array, depth != depth ? 0 : __builtin_trunc(depth));
  return result;
}

pts_array *pts_array_fill(pts_array *array, const void *value, double start, double end) {
  size_t size = pts_field_kind_size(array->type->element);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  for (size_t i = relative_index(start, length), to = relative_index(end, length); i < to; i++) {
    element_store(buffer->elements + i * size, value, size);
  }
  return array;
}

pts_array *pts_array_from_string(pts_string *string) {
  pts_array *array = pts_array_of(PTS_FIELD_STRING, NULL, 0);
  for (uint32_t i = 0; i < string->byte_len;) {
    unsigned char lead = (unsigned char)string->bytes[i];
    uint32_t length = lead < 0x80 ? 1 : lead >= 0xF0 ? 4 : lead >= 0xE0 ? 3 : 2;
    pts_string *code_point = pts_string_from_utf8(string->bytes + i, length);
    pts_array_push(array, &code_point, 1);
    i += length;
  }
  return array;
}
