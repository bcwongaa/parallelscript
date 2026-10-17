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

const pts_type pts_array_types[] = {
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

/* copy_elements copies count elements from a buffer others may write (one load each) to one only we see. */
static void copy_elements(unsigned char *to, const unsigned char *from, size_t count, size_t size) {
  for (size_t i = 0; i < count; i++) {
    element_load(to + i * size, from + i * size, size);
  }
}

/* is_hole reports whether an element just read is a slot no racing push wrote: a zero pointer or dynamic value. */
static bool is_hole(pts_field_kind kind, const void *element) { return holds_pointers(kind) && *(const uint64_t *)element == 0; }

/* load_buffer and publish_buffer read and replace an array's buffer, publish_length its length. */
static pts_array_buffer *load_buffer(const pts_array *array) { return __atomic_load_n(&array->buffer, __ATOMIC_ACQUIRE); }

static void publish_buffer(pts_array *array, pts_array_buffer *buffer) { __atomic_store_n(&array->buffer, buffer, __ATOMIC_RELEASE); }

static void publish_length(pts_array *array, size_t length) { __atomic_store_n(&array->length, length, __ATOMIC_RELEASE); }

/* readable_length is the length clamped to the capacity of buffer, which a racing push may not have reached. */
static size_t readable_length(const pts_array *array, const pts_array_buffer *buffer) {
  size_t length = pts_array_length_of(array);
  return length < buffer->capacity ? length : buffer->capacity;
}

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
  copy_elements(bigger->elements, buffer->elements, kept, pts_field_kind_size(kind)); /* nobody else sees bigger yet */
  publish_buffer(array, bigger);
  return bigger;
}

pts_array *pts_array_of(const pts_type *type, const void *elements, size_t count) {
  pts_field_kind kind = type->element;
  pts_array *array = pts_object_new(type);
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
  if (pts_is_match_array((const pts_object *)array) && index >= 0 && index < pts_array_length(array)) {
    pts_throw_new(&pts_type_builtin_TypeError, "Expected string, got undefined (group %s of the match took no part in it)", buf);
  }
  pts_throw_new(&pts_type_builtin_RangeError, "Array index %s is out of range (length %zu)" PTS_MISSING_ELEMENT_HINT, buf, pts_array_length_of(array));
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

bool pts_array_load(const pts_array *array, double index, void *out) {
  size_t size = pts_field_kind_size(array->type->element);
  const void *slot = pts_array_slot_or_null(array, index, size);
  if (slot == NULL) {
    return false;
  }
  element_load(out, slot, size);
  return !is_hole(array->type->element, out);
}

pts_value pts_array_element(const pts_array *array, double index) {
  size_t size = pts_field_kind_size(array->type->element);
  const void *slot = pts_array_slot_or_null(array, index, size);
  return slot == NULL ? PTS_VALUE_UNDEFINED : pts_value_of_slot(array->type->element, slot);
}

/* ---------------------------------------------------------------- frozen arrays (D-157) */

/*
 * A frozen array's methods throw at the first write JS's algorithm makes, with Node's
 * message for it: to an element below the length (read only), at or past it (not
 * extensible), a delete of the last element, or a write to the length.
 */

void pts_throw_frozen_element(const pts_array *array, double index) {
  char property[PTS_NUMBER_BUF_SIZE];
  pts_number_format(index, property);
  if (index >= 0 && index < pts_array_length(array)) {
    pts_throw_read_only((const pts_object *)array, property);
  }
  pts_throw_not_extensible(property);
}

static _Noreturn void frozen_length(const pts_array *array) { pts_throw_read_only((const pts_object *)array, "length"); }

static _Noreturn void frozen_delete(const pts_array *array, size_t index) {
  char property[PTS_NUMBER_BUF_SIZE];
  pts_number_format((double)index, property);
  pts_throw_undeletable((const pts_object *)array, property);
}

static inline bool is_frozen(const pts_array *array) { return __builtin_expect(array->type->frozen, 0); }

/* pop's first write to a frozen array of length elements: the length of an empty one, else a delete of the last element. */
static _Noreturn void frozen_pop(const pts_array *array, size_t length) {
  if (length == 0) {
    frozen_length(array);
  }
  frozen_delete(array, length - 1);
}

/* shift's: the length of an empty array, a delete of a lone element, or else a move to element 0. */
static _Noreturn void frozen_shift(const pts_array *array, size_t length) {
  if (length == 0) {
    frozen_length(array);
  }
  if (length == 1) {
    frozen_delete(array, 0);
  }
  pts_throw_frozen_element(array, 0);
}

/* unshift's of count elements: the length if none, else the last element's move up, or in an empty array, the first one's store. */
static _Noreturn void frozen_unshift(const pts_array *array, size_t length, size_t count) {
  if (count == 0) {
    frozen_length(array);
  }
  pts_throw_frozen_element(array, length == 0 ? 0 : (double)(length + count - 1));
}

/* splice's (Array.prototype.splice, steps 15 to 20) at from, deleting deleted elements and inserting count. */
static _Noreturn void frozen_splice(const pts_array *array, size_t length, size_t from, size_t deleted, size_t count) {
  if (count < deleted) {
    if (from < length - deleted) {
      pts_throw_frozen_element(array, (double)(from + count)); /* the first element after them moves down */
    }
    frozen_delete(array, length - 1); /* nothing after them: the last is deleted */
  }
  if (count > deleted && length - deleted > from) {
    pts_throw_frozen_element(array, (double)(length - deleted + count - 1)); /* the last element moves up */
  }
  if (count > 0) {
    pts_throw_frozen_element(array, (double)from);
  }
  frozen_length(array);
}

void pts_array_set(pts_array *array, double index, const void *value) {
  if (is_frozen(array)) {
    pts_throw_frozen_element(array, index);
  }
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
  if (is_frozen(array)) {
    if (count == 0) {
      frozen_length(array);
    }
    pts_throw_frozen_element(array, (double)length);
  }
  pts_array_buffer *buffer = with_room(array, length, length + count);
  for (size_t i = 0; i < count; i++) {
    element_store(buffer->elements + (length + i) * size, (const unsigned char *)values + i * size, size);
  }
  publish_length(array, length + count);
  return (double)(length + count);
}

/*
 * A match's groups that took no part stay undefined where its elements go into a string[]
 * (D-133): a plain one takes match copies' descriptor, whose zero slots are undefined, if
 * source has such a group and array no holes of its own, which would read as undefined too
 * (those groups become holes there). takes_undefined_groups tells before the elements go
 * in; keep_undefined_groups switches after, by a compare-and-swap, so that a freeze racing
 * with it keeps its frozen twin (the groups are then holes).
 */
static bool takes_undefined_groups(const pts_array *array, const pts_array *source) {
  return array->type == &pts_array_types[PTS_FIELD_STRING] && pts_has_undefined_holes(source) && pts_array_has_holes(source) && !pts_array_has_holes(array);
}

static void keep_undefined_groups(pts_array *array) {
  const pts_type *plain = &pts_array_types[PTS_FIELD_STRING];
  __atomic_compare_exchange_n(&array->type, &plain, &pts_type_match_copy, false, __ATOMIC_RELAXED, __ATOMIC_RELAXED);
}

double pts_array_push_all(pts_array *array, const pts_array *values) {
  bool keep = takes_undefined_groups(array, values);
  double length = pts_array_push(array, load_buffer(values)->elements, pts_array_length_of(values));
  if (keep) {
    keep_undefined_groups(array);
  }
  return length;
}

double pts_array_unshift_all(pts_array *array, const pts_array *values) {
  bool keep = takes_undefined_groups(array, values);
  double length = pts_array_unshift(array, load_buffer(values)->elements, pts_array_length_of(values));
  if (keep) {
    keep_undefined_groups(array);
  }
  return length;
}

double pts_array_unshift(pts_array *array, const void *values, size_t count) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  size_t length = pts_array_length_of(array);
  if (is_frozen(array)) {
    frozen_unshift(array, length, count);
  }
  pts_array_buffer *old = load_buffer(array);
  size_t kept = length < old->capacity ? length : old->capacity;
  /* A new buffer, filled before it is published, so that readers never see elements half moved. */
  size_t capacity = kept + count > old->capacity ? kept + count : old->capacity;
  pts_array_buffer *buffer = new_buffer(kind, capacity);
  memcpy(buffer->elements, values, count * size);
  copy_elements(buffer->elements + count * size, old->elements, kept, size);
  publish_buffer(array, buffer);
  publish_length(array, kept + count);
  return (double)(kept + count);
}

/* pop and shift: the element removed goes to out; a hole throws (where an element is required) unless holes_undefined, which removes it and leaves out zero. */
static void pop_into(pts_array *array, void *out, bool holes_undefined) {
  size_t length = pts_array_length_of(array);
  if (is_frozen(array)) {
    frozen_pop(array, length);
  }
  if (length == 0) {
    pts_throw_new(&pts_type_builtin_RangeError, "pop() of an empty array");
  }
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  element_load(out, pts_array_slot(array, (double)(length - 1), size), size);
  if (is_hole(kind, out) && !holes_undefined) {
    pts_array_index_error(array, (double)(length - 1));
  }
  publish_length(array, length - 1); /* the slot keeps its element, which racing readers may still read */
}

static void shift_into(pts_array *array, void *out, bool holes_undefined) {
  size_t length = pts_array_length_of(array);
  if (is_frozen(array)) {
    frozen_shift(array, length);
  }
  if (length == 0) {
    pts_throw_new(&pts_type_builtin_RangeError, "shift() of an empty array");
  }
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *buffer = load_buffer(array);
  if (length > buffer->capacity) {
    length = buffer->capacity; /* the length of a buffer published later; this one holds less */
  }
  if (length == 0) {
    pts_throw_new(&pts_type_builtin_RangeError, "shift() of an empty array");
  }
  element_load(out, buffer->elements, size);
  if (is_hole(kind, out) && !holes_undefined) {
    pts_array_index_error(array, 0);
  }
  for (size_t i = 1; i < length; i++) {
    uint64_t element;
    element_load(&element, buffer->elements + i * size, size);
    element_store(buffer->elements + (i - 1) * size, &element, size);
  }
  publish_length(array, length - 1);
}

void pts_array_pop(pts_array *array, void *out) { pop_into(array, out, false); }

void pts_array_shift(pts_array *array, void *out) { shift_into(array, out, false); }

/* JS's pop and shift remove a hole too and give undefined for it (D-183), as they do for an empty array. */
pts_value pts_array_pop_value(pts_array *array) {
  if (pts_array_length_of(array) == 0 && !is_frozen(array)) {
    return PTS_VALUE_UNDEFINED;
  }
  uint64_t element = 0;
  pop_into(array, &element, true);
  return pts_value_of_slot(array->type->element, &element);
}

pts_value pts_array_shift_value(pts_array *array) {
  if (pts_array_length_of(array) == 0 && !is_frozen(array)) {
    return PTS_VALUE_UNDEFINED;
  }
  uint64_t element = 0;
  shift_into(array, &element, true);
  return pts_value_of_slot(array->type->element, &element);
}

double pts_array_set_length(pts_array *array, double length) {
  size_t current = pts_array_length_of(array);
  if (!(length >= 0 && length <= 4294967295.0) || (double)(size_t)length != length) {
    pts_throw_new(&pts_type_builtin_RangeError, "Invalid array length");
  }
  if (is_frozen(array)) {
    frozen_length(array);
  }
  if ((size_t)length > current) {
    char buf[PTS_NUMBER_BUF_SIZE];
    pts_number_format(length, buf);
    pts_throw_new(&pts_type_builtin_RangeError, "Invalid array length %s: setting length can only shrink an array (length %zu)", buf, current);
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

/* new_array makes an array of descriptor type holding count elements read from from. */
static pts_array *new_array(const pts_type *type, const unsigned char *from, size_t count) {
  pts_field_kind kind = type->element;
  pts_array *array = pts_object_new(type);
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
  return new_array(pts_array_copy_type(array->type), buffer->elements + from * size, to > from ? to - from : 0);
}

pts_array *pts_array_copy(const pts_array *array) { return pts_array_slice(array, 0, (double)SIZE_MAX); }

pts_array *pts_array_copy_as_read(const pts_array *array) {
  pts_array *copy = pts_array_copy(array);
  if (copy->type->element == PTS_FIELD_VALUE) {
    pts_value *slots = (pts_value *)(void *)copy->buffer->elements; /* a new array only we see */
    for (size_t i = 0; i < copy->length; i++) {
      if (slots[i] == 0) {
        slots[i] = PTS_VALUE_UNDEFINED;
      }
    }
  }
  return copy;
}

pts_array *pts_array_splice(pts_array *array, double start, double delete_count, const void *items, size_t count) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *old = load_buffer(array);
  size_t length = readable_length(array, old);
  size_t from = relative_index(start, length);
  double wanted = delete_count != delete_count ? 0 : __builtin_trunc(delete_count);
  size_t deleted = wanted <= 0 ? 0 : wanted >= (double)(length - from) ? length - from : (size_t)wanted;
  if (is_frozen(array)) {
    frozen_splice(array, length, from, deleted, count);
  }
  pts_array *removed = new_array(pts_array_copy_type(array->type), old->elements + from * size, deleted);
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

/* Appends other's elements to array, each hole undefined if undefined_holes (dynamic values) or else kept. */
static void append(pts_array *array, const pts_array *other, bool undefined_holes) {
  bool keep = takes_undefined_groups(array, other);
  size_t size = pts_field_kind_size(array->type->element);
  pts_array_buffer *source = load_buffer(other);
  size_t count = readable_length(other, source);
  size_t length = pts_array_length_of(array);
  pts_array_buffer *buffer = with_room(array, length, length + count);
  for (size_t i = 0; i < count; i++) {
    uint64_t element = 0;
    element_load(&element, source->elements + i * size, size);
    if (undefined_holes && element == 0) {
      element = PTS_VALUE_UNDEFINED;
    }
    element_store(buffer->elements + (length + i) * size, &element, size);
  }
  publish_length(array, length + count);
  if (keep) {
    keep_undefined_groups(array);
  }
}

void pts_array_append(pts_array *array, const pts_array *other) { append(array, other, false); }

void pts_array_append_as_read(pts_array *array, const pts_array *other) { append(array, other, array->type->element == PTS_FIELD_VALUE); }

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
    if (is_hole(kind, &element)) {
      if (zero && kind == PTS_FIELD_VALUE && *(const pts_value *)value == PTS_VALUE_UNDEFINED) {
        return (double)i; /* includes reads a hole as undefined; indexOf and lastIndexOf skip it */
      }
      continue;
    }
    if (same_value(kind, &element, value, zero)) {
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
  if (object->type->kind == PTS_KIND_DATE) {
    return pts_date_format((pts_date *)object, PTS_DATE_STRING);
  }
  if (object->type->kind == PTS_KIND_REGEXP) {
    return pts_regexp_to_string((const pts_regexp *)object);
  }
  if (object->type->kind == PTS_KIND_MAP || object->type->kind == PTS_KIND_SET) {
    return pts_string_from_cstr(object->type->kind == PTS_KIND_MAP ? "[object Map]" : "[object Set]");
  }
  if (object->type->kind == PTS_KIND_TUPLE) { /* an array to JS: its elements joined by commas (D-089) */
    pts_string *joined = pts_string_from_cstr("");
    for (size_t i = 0, length = pts_tuple_length(object); i < length; i++) {
      const pts_field *field = &object->type->fields[i];
      pts_value element = pts_value_of_slot(field->kind, (const char *)object + field->offset);
      if (i > 0) {
        joined = pts_string_concat(joined, pts_string_from_cstr(","));
      }
      if (!pts_value_is_nullish(element)) {
        joined = pts_string_concat(joined, pts_value_to_string(element));
      }
    }
    return joined;
  }
  if (object->type->kind == PTS_KIND_OBJECT) {
    return pts_program_object_to_string(object);
  }
  return pts_string_from_cstr("[object Object]");
}

/* element_string is the string join uses for the element at slot, "" for undefined, null and holes; sort's (sort_string) for the others. */
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
  if (is_frozen(array) && length >= 2) {
    pts_throw_frozen_element(array, 0);
  }
  for (size_t i = 0; i < length / 2; i++) {
    uint64_t low = 0, high = 0;
    element_load(&low, buffer->elements + i * size, size);
    element_load(&high, buffer->elements + (length - 1 - i) * size, size);
    element_store(buffer->elements + i * size, &high, size);
    element_store(buffer->elements + (length - 1 - i) * size, &low, size);
  }
  return array;
}

/*
 * Where sort puts an element (SortCompare, SortIndexedProperties): among those it compares,
 * or after them, undefined first and holes last. A match's group that took no part is a
 * zero string slot, undefined to JS (D-133), so it goes last as a hole does and stays one.
 */
typedef enum sort_place { SORT_COMPARED, SORT_UNDEFINED, SORT_HOLE } sort_place;

static sort_place sort_place_of(pts_field_kind kind, const uint64_t *element) {
  if (is_hole(kind, element)) {
    return SORT_HOLE;
  }
  return kind == PTS_FIELD_VALUE && *(const pts_value *)element == PTS_VALUE_UNDEFINED ? SORT_UNDEFINED : SORT_COMPARED;
}

/* The slot sort leaves n places after the elements it compared: undefined while there are undefineds, then a hole. */
static uint64_t sorted_tail(size_t n, size_t undefineds) { return n < undefineds ? (uint64_t)PTS_VALUE_UNDEFINED : 0; }

/* A sort entry: an element and the string it sorts by. */
typedef struct sort_entry {
  pts_string *key;
  uint64_t element;
} sort_entry;

/* sort_string is ToString of an element sort compares, which is never undefined or a hole: null is "null". */
static pts_string *sort_string(pts_field_kind kind, const uint64_t *element) {
  if (kind == PTS_FIELD_VALUE && *(const pts_value *)element == PTS_VALUE_NULL) {
    return pts_string_from_cstr("null");
  }
  return element_string(kind, element);
}

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
  size_t count = 0, undefineds = 0;
  for (size_t i = 0; i < length; i++) {
    uint64_t element = 0;
    element_load(&element, buffer->elements + i * size, size);
    sort_place place = sort_place_of(kind, &element);
    if (place == SORT_COMPARED) {
      entries[count].element = element;
      entries[count++].key = sort_string(kind, &element);
    } else if (place == SORT_UNDEFINED) {
      undefineds++;
    }
  }
  merge_sort(entries, entries + length, count);
  if (is_frozen(array) && length >= 2 && count + undefineds > 0) {
    pts_throw_frozen_element(array, 0); /* after the elements' strings, as JS writes them back; holes alone it only deletes */
  }
  /* The elements' toString methods may have changed the array: write to its current buffer, as far as it reaches. */
  buffer = load_buffer(array);
  size_t now = readable_length(array, buffer);
  for (size_t i = 0; i < length && i < now; i++) {
    uint64_t element = i < count ? entries[i].element : sorted_tail(i - count, undefineds);
    element_store(buffer->elements + i * size, &element, size);
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
  /* Scanned memory: the elements may be pointers the collector must keep. */
  uint64_t *elements = GC_MALLOC(2 * length * sizeof *elements + 1);
  if (elements == NULL) {
    pts_panic("RangeError", "Out of memory sorting an array of %zu elements", length);
  }
  size_t count = 0, undefineds = 0;
  for (size_t i = 0; i < length; i++) {
    uint64_t element = 0;
    element_load(&element, buffer->elements + i * size, size);
    sort_place place = sort_place_of(kind, &element);
    if (place == SORT_COMPARED) {
      elements[count++] = element; /* compare never sees undefined or a hole */
    } else if (place == SORT_UNDEFINED) {
      undefineds++;
    }
  }
  compare_sort(elements, elements + length, count, compare, call);
  if (is_frozen(array) && length >= 2 && count + undefineds > 0) {
    pts_throw_frozen_element(array, 0); /* after the comparator's calls, as JS writes them back; holes alone it only deletes */
  }
  /* The comparator may have changed the array: write to its current buffer, as far as it reaches. */
  buffer = load_buffer(array);
  size_t now = readable_length(array, buffer);
  for (size_t i = 0; i < length && i < now; i++) {
    uint64_t element = i < count ? elements[i] : sorted_tail(i - count, undefineds);
    element_store(buffer->elements + i * size, &element, size);
  }
  return array;
}

size_t pts_array_gather_sortable(pts_array *array) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  if (!holds_pointers(kind)) {
    return length; /* numbers and booleans: neither holes nor undefined (D-085) */
  }
  size_t count = 0, undefineds = 0;
  for (size_t i = 0; i < length; i++) {
    uint64_t element = 0;
    element_load(&element, buffer->elements + i * size, size);
    sort_place place = sort_place_of(kind, &element);
    if (place == SORT_COMPARED) {
      element_store(buffer->elements + count++ * size, &element, size); /* to a slot read already */
    } else if (place == SORT_UNDEFINED) {
      undefineds++;
    }
  }
  for (size_t i = count; i < length; i++) {
    uint64_t element = sorted_tail(i - count, undefineds);
    element_store(buffer->elements + i * size, &element, size);
  }
  return count;
}

void pts_array_write_sorted(pts_array *array, const pts_array *sorted) {
  size_t size = pts_field_kind_size(array->type->element);
  pts_array_buffer *from = load_buffer(sorted);
  size_t length = readable_length(sorted, from);
  if (is_frozen(array) && length >= 2) {
    for (size_t i = 0; i < length; i++) {
      uint64_t element = 0;
      element_load(&element, from->elements + i * size, size);
      if (!is_hole(array->type->element, &element)) {
        pts_throw_frozen_element(array, 0); /* as sort's first write; holes alone it only deletes */
      }
    }
  }
  pts_array_buffer *buffer = load_buffer(array);
  size_t now = readable_length(array, buffer);
  for (size_t i = 0; i < length && i < now; i++) {
    uint64_t element = 0;
    element_load(&element, from->elements + i * size, size);
    element_store(buffer->elements + i * size, &element, size);
  }
}

double pts_parallel_gather_sortable(pts_value array) { return (double)pts_array_gather_sortable((pts_array *)pts_value_as_object(array)); }

void pts_parallel_write_sorted(pts_value array, pts_value sorted) {
  pts_array_write_sorted((pts_array *)pts_value_as_object(array), (const pts_array *)pts_value_as_object(sorted));
}

/*
 * flat and flatMap (D-180) follow JS's FlattenIntoArray over ParallelScript's
 * representations: an element in a slot of any kind that is an array or a tuple (JS's
 * IsArray) is flattened while depth lasts, holes are skipped at every level (a match's
 * groups that took no part are undefined elements, not holes, D-133), and each other
 * element is a leaf, stored as the result's element type holds it: as it is from slots of
 * that type, else converted and checked, a TypeError if it isn't one, since tsc's type for
 * the result can be wrong (`flatMap<number[]>((x) => [x])` holds numbers in JS).
 */

static void flatten_into(pts_array *result, const pts_object *source, double depth);

/* The array or tuple a slot's element is, which flat spreads, or NULL. */
static const pts_object *flattened_object(pts_field_kind kind, const void *element) {
  if (kind != PTS_FIELD_OBJECT && kind != PTS_FIELD_VALUE) {
    return NULL;
  }
  pts_value value = *(const pts_value *)element; /* a pointer is its own value (D-002) */
  if (!pts_value_is_object(value)) {
    return NULL;
  }
  const pts_object *object = pts_value_as_object(value);
  return object->type->kind == PTS_KIND_ARRAY || object->type->kind == PTS_KIND_TUPLE ? object : NULL;
}

/* How flatten takes the elements of slots of one kind and declared type in an array or tuple. */
typedef struct flattened_slots {
  pts_field_kind kind;
  bool of_type;         /* their leaves are of the result's element type, if perhaps held in another kind */
  bool as_is;           /* their leaves are elements of the result as they are: of its type and kind */
  bool undefined_holes; /* a zero word is undefined: a match's group that took no part */
} flattened_slots;

/* How flatten takes slots of kind declared `declared` (NULL: the type the kind holds) of source into result. */
static flattened_slots slots_of(const pts_array *result, const pts_object *source, pts_field_kind kind, const pts_check *declared) {
  const pts_check *into = result->type->check; /* NULL: the type result's element kind holds */
  bool of_type = into == NULL ? kind == result->type->element : pts_check_implies(pts_check_declared(kind, declared), into);
  bool undefined_holes = kind == PTS_FIELD_STRING && source->type->kind == PTS_KIND_ARRAY && pts_has_undefined_holes((const pts_array *)source);
  return (flattened_slots){.kind = kind, .of_type = of_type, .as_is = of_type && kind == result->type->element, .undefined_holes = undefined_holes};
}

/* Whether a slot of kind can hold value as it is, for a result without a declared element type: a leaf is converted, never reinterpreted. */
static bool kind_holds(pts_field_kind kind, pts_value value) {
  switch (kind) {
  case PTS_FIELD_VALUE:
    return true;
  case PTS_FIELD_NUMBER:
  case PTS_FIELD_F32:
    return pts_value_is_number(value);
  case PTS_FIELD_BOOL:
    return value == PTS_VALUE_TRUE || value == PTS_VALUE_FALSE;
  case PTS_FIELD_STRING:
    return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_STRING;
  case PTS_FIELD_OBJECT:
    return pts_value_is_object(value) && pts_value_as_object(value)->type->kind != PTS_KIND_STRING;
  default:
    return pts_value_is_integer_of(value, kind);
  }
}

/* Whether value is a boxed 64-bit integer, which a number slot holds as the number nearest it (D-012). */
static bool is_boxed_integer(pts_value value) {
  if (!pts_value_is_object(value)) {
    return false;
  }
  pts_kind kind = pts_value_as_object(value)->type->kind;
  return kind == PTS_KIND_INT64 || kind == PTS_KIND_UINT64;
}

/* Whether a place declared check holds a boxed 64-bit integer as it is: any, a 64-bit integer type or a union with one; elsewhere, where a number goes, it is the number nearest it (D-012). */
static bool holds_boxed_integers(const pts_check *check) {
  switch (check->kind) {
  case PTS_CHECK_ANY:
    return true;
  case PTS_CHECK_INTEGER:
    return check->slot == PTS_FIELD_I64 || check->slot == PTS_FIELD_U64;
  case PTS_CHECK_UNION:
    for (uint32_t i = 0; i < check->count; i++) {
      if (holds_boxed_integers(check->members[i])) {
        return true;
      }
    }
    return false;
  default:
    return false;
  }
}

/* The last check and object type a leaf was found to be of as it is, on this thread: the leaves of one array are mostly of one class, and whether an object is of an object type depends on its descriptor alone. */
static _Thread_local struct {
  const pts_check *check;
  const pts_type *type;
} last_fit;

/* value as a leaf of a result declared check: itself if it is one (as last_fit says or the conversion finds), a copy of dynamic data, TypeError if neither. */
static pts_value leaf_of(const pts_check *check, pts_value value) {
  const pts_object *object = pts_value_is_object(value) ? pts_value_as_object(value) : NULL;
  if (object != NULL && last_fit.check == check && last_fit.type == object->type) {
    return value;
  }
  pts_value converted = pts_value_convert(value, check);
  if (converted == value && object != NULL && object->type->kind == PTS_KIND_OBJECT) {
    last_fit.check = check;
    last_fit.type = object->type;
  }
  return converted;
}

/* Pushes a leaf from a slot of another kind or type onto result: converted to its element type (checked unless of_type, which says it is one), TypeError if it isn't one. */
static void push_leaf(pts_array *result, pts_value value, bool of_type) {
  const pts_type *type = result->type;
  pts_field_kind into = type->element;
  uint64_t element = 0;
  if (value == PTS_VALUE_UNDEFINED && into == PTS_FIELD_STRING) {
    result->type = &pts_type_match_copy; /* a match's group that took no part: a string[] holds it as copies of matches do (D-133) */
    pts_array_push(result, &element, 1);
    return;
  }
  if (type->check != NULL) {
    if (is_boxed_integer(value) && !holds_boxed_integers(type->check)) {
      value = pts_value_from_number(pts_value_to_number(value));
    }
    value = of_type ? value : leaf_of(type->check, value);
  } else if (!kind_holds(into, value)) {
    if ((into != PTS_FIELD_NUMBER && into != PTS_FIELD_F32) || !is_boxed_integer(value)) {
      pts_throw_type_mismatch(value, pts_check_declared(into, NULL)->name);
    }
    value = pts_value_from_number(pts_value_to_number(value));
  }
  pts_store_value_in_slot(into, &element, value);
  pts_array_push(result, &element, 1);
}

/* Pushes the element in slot onto result: an array's or tuple's elements depth levels down, nothing for a hole. */
static void flatten_element(pts_array *result, flattened_slots slots, const void *slot, double depth) {
  uint64_t element = 0;
  element_load(&element, slot, pts_field_kind_size(slots.kind));
  if (is_hole(slots.kind, &element)) {
    if (slots.undefined_holes) {
      push_leaf(result, PTS_VALUE_UNDEFINED, false);
    }
    return;
  }
  const pts_object *nested = depth >= 1 ? flattened_object(slots.kind, &element) : NULL;
  if (nested != NULL) {
    flatten_into(result, nested, depth - 1);
  } else if (slots.as_is) {
    pts_array_push(result, &element, 1);
  } else {
    push_leaf(result, pts_value_of_slot(slots.kind, &element), slots.of_type);
  }
}

/* Appends count elements of result's kind, loaded one at a time from a buffer others may write, to result, which only this thread sees. */
static void append_loaded(pts_array *result, const unsigned char *from, size_t count, size_t size) {
  size_t length = pts_array_length_of(result);
  pts_array_buffer *buffer = with_room(result, length, length + count);
  copy_elements(buffer->elements + length * size, from, count, size);
  publish_length(result, length + count);
}

/* append_loaded for pointers and dynamic values: the elements that aren't holes. */
static void append_present(pts_array *result, const unsigned char *from, size_t count) {
  size_t length = pts_array_length_of(result);
  pts_array_buffer *buffer = with_room(result, length, length + count);
  size_t stored = length;
  for (size_t i = 0; i < count; i++) {
    uint64_t element;
    element_load(&element, from + i * sizeof element, sizeof element);
    if (element != 0) {
      element_store(buffer->elements + stored++ * sizeof element, &element, sizeof element);
    }
  }
  publish_length(result, stored);
}

/* Pushes array's elements below limit onto result, each flattened depth levels more. */
static void flatten_array(pts_array *result, const pts_array *array, size_t limit, double depth) {
  pts_field_kind kind = array->type->element;
  size_t size = pts_field_kind_size(kind);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  if (length > limit) {
    length = limit;
  }
  if (kind == result->type->element && !holds_pointers(kind)) {
    append_loaded(result, buffer->elements, length, size); /* numbers or booleans of the result's kind: no holes, no arrays, no checks */
    return;
  }
  flattened_slots slots = slots_of(result, (const pts_object *)array, kind, array->type->check);
  if (slots.as_is && !slots.undefined_holes && (depth < 1 || slots.kind == PTS_FIELD_STRING)) {
    append_present(result, buffer->elements, length); /* leaves only, holes left out */
    return;
  }
  for (size_t i = 0; i < length; i++) {
    flatten_element(result, slots, buffer->elements + i * size, depth);
  }
}

/* Pushes the elements of source, an array or a tuple, onto result, each flattened depth levels more. */
static void flatten_into(pts_array *result, const pts_object *source, double depth) {
  pts_stack_check(); /* deep or cyclic nesting throws RangeError, as in JS, rather than overflow the C stack */
  if (source->type->kind != PTS_KIND_TUPLE) {
    flatten_array(result, (const pts_array *)source, SIZE_MAX, depth);
    return;
  }
  for (size_t i = 0, length = pts_tuple_length(source); i < length; i++) {
    const pts_field *field = &source->type->fields[i]; /* a tuple's fields are its elements, in order */
    flatten_element(result, slots_of(result, source, field->kind, field->check), (const char *)source + field->offset, depth);
  }
}

/* ToIntegerOrInfinity of a depth that is a number. */
static double integer_or_infinity(double depth) { return depth != depth ? 0 : __builtin_trunc(depth); }

pts_array *pts_array_flat(const pts_array *array, double depth, const pts_type *result_type) {
  pts_array *result = pts_array_of(result_type, NULL, 0);
  flatten_into(result, (const pts_object *)array, integer_or_infinity(depth));
  return result;
}

pts_array *pts_array_flat_value(const pts_array *array, pts_value depth, const pts_type *result_type) {
  size_t length = pts_array_length_of(array); /* read before the depth's valueOf, which may change the array, runs, as in JS */
  double levels = depth == PTS_VALUE_UNDEFINED ? 1 : pts_value_to_number(depth);
  pts_array *result = pts_array_of(result_type, NULL, 0);
  flatten_array(result, array, length, integer_or_infinity(levels));
  return result;
}

void pts_array_push_flat(pts_array *result, pts_value value) {
  bool any = result->type->element == PTS_FIELD_VALUE && result->type->check == NULL;
  flattened_slots slots = {.kind = PTS_FIELD_VALUE, .of_type = any, .as_is = any};
  flatten_element(result, slots, &value, 1);
}

bool pts_parallel_has_holes_in(pts_value array, double from, double to) { return pts_array_has_holes_in((const pts_array *)pts_value_as_object(array), from, to); }

bool pts_parallel_holds_pointers(pts_value array) { return holds_pointers(((const pts_array *)pts_value_as_object(array))->type->element); }

void pts_parallel_push_flat(pts_value result, pts_value value) { pts_array_push_flat((pts_array *)pts_value_as_object(result), value); }

bool pts_array_has_holes_in(const pts_array *array, double from, double to) {
  if (!holds_pointers(array->type->element)) {
    return false;
  }
  pts_array_buffer *buffer = load_buffer(array);
  const uint64_t *slots = (const uint64_t *)(const void *)buffer->elements;
  size_t length = readable_length(array, buffer);
  size_t end = to < (double)length ? (size_t)to : length;
  size_t i = from > 0 ? (size_t)from : 0;
  for (; i + 4 <= end; i += 4) { /* four slots a test: the methods that skip holes pay this per element */
    uint64_t a = __atomic_load_n(slots + i, __ATOMIC_RELAXED), b = __atomic_load_n(slots + i + 1, __ATOMIC_RELAXED);
    uint64_t c = __atomic_load_n(slots + i + 2, __ATOMIC_RELAXED), d = __atomic_load_n(slots + i + 3, __ATOMIC_RELAXED);
    if ((a == 0) | (b == 0) | (c == 0) | (d == 0)) {
      return true;
    }
  }
  for (; i < end; i++) {
    if (__atomic_load_n(slots + i, __ATOMIC_RELAXED) == 0) {
      return true;
    }
  }
  return false;
}

bool pts_array_has_holes(const pts_array *array) { return pts_array_has_holes_in(array, 0, (double)SIZE_MAX); }

pts_array *pts_array_fill(pts_array *array, const void *value, double start, double end) {
  size_t size = pts_field_kind_size(array->type->element);
  pts_array_buffer *buffer = load_buffer(array);
  size_t length = readable_length(array, buffer);
  size_t from = relative_index(start, length), to = relative_index(end, length);
  if (is_frozen(array) && from < to) {
    pts_throw_frozen_element(array, (double)from);
  }
  for (size_t i = from; i < to; i++) {
    element_store(buffer->elements + i * size, value, size);
  }
  return array;
}

pts_array *pts_array_from_string(pts_string *string) {
  pts_array *array = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  for (uint32_t i = 0; i < string->byte_len;) {
    unsigned char lead = (unsigned char)string->bytes[i];
    uint32_t length = lead < 0x80 ? 1 : lead >= 0xF0 ? 4 : lead >= 0xE0 ? 3 : 2;
    pts_string *code_point = pts_string_from_utf8(string->bytes + i, length);
    pts_array_push(array, &code_point, 1);
    i += length;
  }
  return array;
}

/* ---------------------------------------------------------------- M3.5c2 (D-085) */

pts_value pts_array_at(const pts_array *array, double index) {
  double position = index != index ? 0 : __builtin_trunc(index);
  return pts_array_element(array, position < 0 ? position + pts_array_length(array) : position);
}

pts_array *pts_array_with(const pts_array *array, double index, const void *value, bool undefined_holes) {
  double position = index != index ? 0 : __builtin_trunc(index);
  double length = pts_array_length(array);
  double at = position < 0 ? position + length : position;
  if (!(at >= 0 && at < length)) {
    char buf[PTS_NUMBER_BUF_SIZE];
    pts_number_format(position + 0.0, buf);
    pts_throw_new(&pts_type_builtin_RangeError, "Invalid index : %s", buf);
  }
  pts_array *copy = undefined_holes ? pts_array_copy_as_read(array) : pts_array_copy(array);
  if ((size_t)at >= copy->length) {
    pts_array_index_error(copy, at); /* another thread shrank the array since its length was read */
  }
  size_t size = pts_field_kind_size(array->type->element);
  element_store(copy->buffer->elements + (size_t)at * size, value, size);
  return copy;
}

pts_array *pts_array_of_length(const pts_type *type, double length) {
  if (!(length >= 0 && length <= 4294967295.0) || length != __builtin_trunc(length)) {
    pts_throw_new(&pts_type_builtin_RangeError, "Invalid array length");
  }
  pts_array *array = pts_object_new(type);
  array->buffer = new_buffer(type->element, (size_t)length); /* zeroed: holes, or zeros of a primitive kind */
  array->length = (size_t)length;
  return array;
}

pts_array *pts_array_keys(const pts_array *array) {
  size_t length = pts_array_length_of(array);
  pts_array *keys = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], NULL, 0);
  keys->buffer = new_buffer(PTS_FIELD_NUMBER, length);
  for (size_t i = 0; i < length; i++) {
    ((double *)keys->buffer->elements)[i] = (double)i;
  }
  keys->length = length;
  return keys;
}
