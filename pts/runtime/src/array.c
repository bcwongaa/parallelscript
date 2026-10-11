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

/* box boxes the element at slot, of the given kind, as a dynamic value. */
static pts_value box(pts_field_kind kind, const void *slot) {
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

pts_value pts_array_element(const pts_array *array, double index) {
  size_t size = pts_field_kind_size(array->type->element);
  const void *slot = pts_array_slot_or_null(array, index, size);
  return slot == NULL ? PTS_VALUE_UNDEFINED : box(array->type->element, slot);
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
  return box(array->type->element, &element);
}

pts_value pts_array_shift_value(pts_array *array) {
  if (pts_array_length_of(array) == 0) {
    return PTS_VALUE_UNDEFINED;
  }
  uint64_t element;
  pts_array_shift(array, &element);
  return box(array->type->element, &element);
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
