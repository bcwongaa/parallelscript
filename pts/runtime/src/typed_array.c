/*
 * Typed arrays and ArrayBuffer (D-125): fixed-length views of a buffer's bytes, with JS's
 * conversions of the numbers written to them. Elements hold no pointers, so the methods
 * copy them with memcpy and memmove; racing threads can see torn or stale numbers in an
 * element, never bytes outside the view (D-L13).
 */
#include "pts.h"

#include <math.h>
#include <stdlib.h>
#include <string.h>

const pts_type pts_type_builtin_ArrayBuffer = {
    .kind = PTS_KIND_ARRAY_BUFFER,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "ArrayBuffer",
    .size = sizeof(pts_arraybuffer),
};

#define TYPED_ARRAY_TYPE(class_name, ctype, element_kind, convert)              \
  const pts_type pts_type_builtin_##class_name = {                              \
      .kind = PTS_KIND_TYPED_ARRAY,                                             \
      .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_typed_array, buffer)),   \
      .name = #class_name,                                                      \
      .size = sizeof(pts_typed_array),                                          \
      .element = element_kind,                                                  \
  };
PTS_TYPED_ARRAYS(TYPED_ARRAY_TYPE)
#undef TYPED_ARRAY_TYPE

/* The largest length, in bytes, of a buffer: V8's on 64-bit hosts. */
static const double MAX_BYTE_LENGTH = 9007199254740991.0;

static size_t element_size(const pts_typed_array *array) { return pts_field_kind_size(array->type->element); }

void pts_typed_array_index_error(const pts_typed_array *array, double index) {
  char buf[PTS_NUMBER_BUF_SIZE];
  pts_number_format(index, buf);
  pts_throw_new(&pts_type_builtin_RangeError, "%s index %s is out of range (length %zu)", array->type->name, buf, array->length);
}

/* JS's ToIntegerOrInfinity. */
static double to_integer(double d) {
  if (d != d) {
    return 0;
  }
  return trunc(d) + 0.0; /* -0 is 0 */
}

/* A relative index (start or end of a slice) resolved against length: from the end if negative, clamped to [0, length]. */
static size_t relative_index(double index, size_t length) {
  double i = to_integer(index);
  if (i < 0) {
    i += (double)length;
    return i < 0 ? 0 : (size_t)i;
  }
  return i > (double)length ? length : (size_t)i;
}

static pts_arraybuffer *new_buffer(size_t byte_length) {
  pts_arraybuffer *buffer = GC_MALLOC_ATOMIC(sizeof(pts_arraybuffer) + byte_length);
  if (buffer == NULL) {
    pts_throw_new(&pts_type_builtin_RangeError, "Array buffer allocation failed");
  }
  memset(buffer, 0, sizeof(pts_arraybuffer) + byte_length);
  buffer->type = &pts_type_builtin_ArrayBuffer;
  buffer->byte_length = byte_length;
  return buffer;
}

static pts_typed_array *new_view(const pts_type *type, pts_arraybuffer *buffer, size_t byte_offset, size_t length) {
  pts_typed_array *array = pts_object_new(type);
  array->buffer = buffer;
  array->data = buffer->bytes + byte_offset;
  array->length = length;
  array->byte_offset = byte_offset;
  return array;
}

/* A new typed array of length zeros: the length is a size the caller checked. */
static pts_typed_array *new_typed_array(const pts_type *type, size_t length) {
  return new_view(type, new_buffer(length * pts_field_kind_size(type->element)), 0, length);
}

pts_arraybuffer *pts_arraybuffer_new(double length) {
  double n = to_integer(length);
  if (n < 0 || n > MAX_BYTE_LENGTH) {
    pts_throw_new(&pts_type_builtin_RangeError, "Invalid array buffer length");
  }
  return new_buffer((size_t)n);
}

pts_arraybuffer *pts_arraybuffer_slice(const pts_arraybuffer *buffer, double start, double end) {
  size_t from = relative_index(start, buffer->byte_length);
  size_t to = relative_index(end, buffer->byte_length);
  size_t count = to > from ? to - from : 0;
  pts_arraybuffer *copy = new_buffer(count);
  memcpy(copy->bytes, buffer->bytes + from, count);
  return copy;
}

bool pts_arraybuffer_is_view(pts_value value) {
  return pts_value_is_object(value) && pts_is_typed_array_type(pts_value_as_object(value)->type);
}

pts_typed_array *pts_typed_array_new(const pts_type *type, double length) {
  double n = to_integer(length);
  if (n < 0 || n * (double)pts_field_kind_size(type->element) > MAX_BYTE_LENGTH) {
    char buf[PTS_NUMBER_BUF_SIZE];
    pts_number_format(length, buf);
    pts_throw_new(&pts_type_builtin_RangeError, "Invalid typed array length: %s", buf);
  }
  return new_typed_array(type, (size_t)n);
}

double pts_typed_array_get(const pts_typed_array *array, double index) {
  if (!pts_typed_array_has_index(array, index)) {
    pts_typed_array_index_error(array, index);
  }
  return pts_typed_array_as_number(array, (size_t)index);
}

double pts_typed_array_set(pts_typed_array *array, double index, double value) {
  if (pts_typed_array_has_index(array, index)) {
    pts_typed_array_store(array, (size_t)index, value);
  }
  return value;
}

pts_value pts_typed_array_element(const pts_typed_array *array, double index) {
  if (!pts_typed_array_has_index(array, index)) {
    return PTS_VALUE_UNDEFINED;
  }
  return pts_value_from_number(pts_typed_array_as_number(array, (size_t)index));
}

pts_value pts_typed_array_at(const pts_typed_array *array, double index) {
  double i = to_integer(index);
  if (i < 0) {
    i += (double)array->length;
  }
  return pts_typed_array_element(array, i);
}

/* An array's element i (below its length) as a number, by ToNumber for elements that aren't numbers. */
static double array_number(const pts_array *array, size_t i) {
  pts_field_kind kind = array->type->element;
  const void *slot = pts_array_slot_or_null(array, (double)i, pts_field_kind_size(kind));
  if (slot == NULL) {
    return NAN; /* an element a racing pop removed: undefined */
  }
  if (kind == PTS_FIELD_NUMBER) {
    return *(const double *)slot;
  }
  pts_value element = pts_value_of_slot(kind, slot);
  return pts_value_to_number(element == 0 ? PTS_VALUE_UNDEFINED : element);
}

pts_typed_array *pts_typed_array_from_array(const pts_type *type, const pts_array *source) {
  size_t length = pts_array_length_of(source);
  pts_typed_array *array = new_typed_array(type, length);
  for (size_t i = 0; i < length; i++) {
    pts_typed_array_store(array, i, array_number(source, i));
  }
  return array;
}

pts_typed_array *pts_typed_array_from_typed(const pts_type *type, const pts_typed_array *source) {
  pts_typed_array *array = new_typed_array(type, source->length);
  if (type->element == source->type->element && type != &pts_type_builtin_Uint8ClampedArray) {
    memcpy(array->data, source->data, source->length * element_size(source));
    return array;
  }
  for (size_t i = 0; i < source->length; i++) {
    pts_typed_array_store(array, i, pts_typed_array_as_number(source, i));
  }
  return array;
}

static _Noreturn void offset_error(double offset) {
  char buf[PTS_NUMBER_BUF_SIZE];
  pts_number_format(offset, buf);
  pts_throw_new(&pts_type_builtin_RangeError, "Start offset %s is outside the bounds of the buffer", buf);
}

pts_typed_array *pts_typed_array_on_buffer(const pts_type *type, pts_arraybuffer *buffer, double byte_offset, double length, bool length_given) {
  size_t size = pts_field_kind_size(type->element);
  double offset = to_integer(byte_offset);
  if (offset < 0 || offset > MAX_BYTE_LENGTH) {
    offset_error(byte_offset);
  }
  if (fmod(offset, (double)size) != 0) {
    pts_throw_new(&pts_type_builtin_RangeError, "start offset of %s should be a multiple of %zu", type->name, size);
  }
  if (!length_given) {
    if (buffer->byte_length % size != 0) {
      pts_throw_new(&pts_type_builtin_RangeError, "byte length of %s should be a multiple of %zu", type->name, size);
    }
    if (offset > (double)buffer->byte_length) {
      offset_error(offset);
    }
    return new_view(type, buffer, (size_t)offset, (buffer->byte_length - (size_t)offset) / size);
  }
  double n = to_integer(length);
  if (n < 0 || offset + n * (double)size > (double)buffer->byte_length) {
    char buf[PTS_NUMBER_BUF_SIZE];
    pts_number_format(length, buf);
    pts_throw_new(&pts_type_builtin_RangeError, "Invalid typed array length: %s", buf);
  }
  return new_view(type, buffer, (size_t)offset, (size_t)n);
}

pts_typed_array *pts_typed_array_subarray(const pts_typed_array *array, double begin, double end) {
  size_t from = relative_index(begin, array->length);
  size_t to = relative_index(end, array->length);
  size_t length = to > from ? to - from : 0;
  return new_view(array->type, array->buffer, array->byte_offset + from * element_size(array), length);
}

pts_typed_array *pts_typed_array_slice(const pts_typed_array *array, double start, double end) {
  size_t from = relative_index(start, array->length);
  size_t to = relative_index(end, array->length);
  size_t length = to > from ? to - from : 0;
  pts_typed_array *copy = new_typed_array(array->type, length);
  memcpy(copy->data, array->data + from * element_size(array), length * element_size(array));
  return copy;
}

pts_typed_array *pts_typed_array_fill(pts_typed_array *array, double value, double start, double end) {
  size_t from = relative_index(start, array->length);
  size_t to = relative_index(end, array->length);
  for (size_t i = from; i < to; i++) {
    pts_typed_array_store(array, i, value);
  }
  return array;
}

pts_typed_array *pts_typed_array_copy_within(pts_typed_array *array, double target, double start, double end) {
  size_t length = array->length;
  size_t to = relative_index(target, length);
  size_t from = relative_index(start, length);
  size_t final = relative_index(end, length);
  size_t count = final > from ? final - from : 0;
  if (count > length - to) {
    count = length - to;
  }
  size_t size = element_size(array);
  memmove(array->data + to * size, array->data + from * size, count * size);
  return array;
}

pts_typed_array *pts_typed_array_reverse(pts_typed_array *array) {
  size_t size = element_size(array);
  unsigned char low[8], high[8];
  for (size_t i = 0, j = array->length; i + 1 < j; i++, j--) {
    memcpy(low, array->data + i * size, size);
    memcpy(high, array->data + (j - 1) * size, size);
    memcpy(array->data + i * size, high, size);
    memcpy(array->data + (j - 1) * size, low, size);
  }
  return array;
}

pts_typed_array *pts_typed_array_to_reversed(const pts_typed_array *array) {
  return pts_typed_array_reverse(pts_typed_array_slice(array, 0, INFINITY));
}

/* Comparators for sort: ascending, -0 before +0, NaN last (TypedArray's default order). */
#define INTEGER_COMPARE(ctype)                                     \
  static int compare_##ctype(const void *a, const void *b) {       \
    ctype x = *(const ctype *)a, y = *(const ctype *)b;            \
    return (x > y) - (x < y);                                      \
  }
INTEGER_COMPARE(int8_t)
INTEGER_COMPARE(uint8_t)
INTEGER_COMPARE(int16_t)
INTEGER_COMPARE(uint16_t)
INTEGER_COMPARE(int32_t)
INTEGER_COMPARE(uint32_t)
#undef INTEGER_COMPARE

static int compare_doubles(double x, double y) {
  if (x != x || y != y) {
    return (x != x) - (y != y);
  }
  if (x == y) {
    return signbit(y) - signbit(x) != 0 ? (signbit(x) ? -1 : 1) : 0;
  }
  return x < y ? -1 : 1;
}

static int compare_double(const void *a, const void *b) { return compare_doubles(*(const double *)a, *(const double *)b); }

static int compare_float(const void *a, const void *b) { return compare_doubles(*(const float *)a, *(const float *)b); }

pts_typed_array *pts_typed_array_sort(pts_typed_array *array) {
  int (*compare)(const void *, const void *) = NULL;
  switch (array->type->element) {
  case PTS_FIELD_I8:
    compare = compare_int8_t;
    break;
  case PTS_FIELD_U8:
    compare = compare_uint8_t;
    break;
  case PTS_FIELD_I16:
    compare = compare_int16_t;
    break;
  case PTS_FIELD_U16:
    compare = compare_uint16_t;
    break;
  case PTS_FIELD_I32:
    compare = compare_int32_t;
    break;
  case PTS_FIELD_U32:
    compare = compare_uint32_t;
    break;
  case PTS_FIELD_F32:
    compare = compare_float;
    break;
  default:
    compare = compare_double;
  }
  qsort(array->data, array->length, element_size(array), compare);
  return array;
}

pts_typed_array *pts_typed_array_to_sorted(const pts_typed_array *array) {
  return pts_typed_array_sort(pts_typed_array_slice(array, 0, INFINITY));
}

pts_typed_array *pts_typed_array_with(const pts_typed_array *array, double index, double value) {
  double i = to_integer(index);
  if (i < 0) {
    i += (double)array->length;
  }
  if (!(i >= 0 && i < (double)array->length)) {
    pts_throw_new(&pts_type_builtin_RangeError, "Invalid typed array index");
  }
  pts_typed_array *copy = pts_typed_array_slice(array, 0, INFINITY);
  pts_typed_array_store(copy, (size_t)i, value);
  return copy;
}

/* The offset of set(source, offset), checked to leave room for count elements. */
static size_t set_offset(const pts_typed_array *array, double offset, size_t count) {
  double at = to_integer(offset);
  if (at < 0 || at + (double)count > (double)array->length) {
    pts_throw_new(&pts_type_builtin_RangeError, "offset is out of bounds");
  }
  return (size_t)at;
}

void pts_typed_array_set_array(pts_typed_array *array, const pts_array *source, double offset) {
  size_t count = pts_array_length_of(source);
  size_t at = set_offset(array, offset, count);
  for (size_t i = 0; i < count; i++) {
    pts_typed_array_store(array, at + i, array_number(source, i));
  }
}

void pts_typed_array_set_typed(pts_typed_array *array, const pts_typed_array *source, double offset) {
  size_t at = set_offset(array, offset, source->length);
  if (array->type->element == source->type->element && array->type != &pts_type_builtin_Uint8ClampedArray) {
    memmove(array->data + at * element_size(array), source->data, source->length * element_size(source));
    return;
  }
  if (source->buffer == array->buffer) { /* overlapping views: read every element before writing any */
    source = pts_typed_array_slice(source, 0, INFINITY);
  }
  for (size_t i = 0; i < source->length; i++) {
    pts_typed_array_store(array, at + i, pts_typed_array_as_number(source, i));
  }
}

/* The index searches start at from (a left-out from is 0): from the end if negative. */
static size_t search_start(double from, size_t length) { return relative_index(from, length); }

double pts_typed_array_index_of(const pts_typed_array *array, double value, double from) {
  for (size_t i = search_start(from, array->length); i < array->length; i++) {
    if (pts_typed_array_as_number(array, i) == value) {
      return (double)i;
    }
  }
  return -1;
}

double pts_typed_array_last_index_of(const pts_typed_array *array, double value, double from) {
  double start = to_integer(from);
  if (start < 0) {
    start += (double)array->length;
  }
  if (start >= (double)array->length) {
    start = (double)array->length - 1;
  }
  for (double i = start; i >= 0; i--) {
    if (pts_typed_array_as_number(array, (size_t)i) == value) {
      return i;
    }
  }
  return -1;
}

bool pts_typed_array_includes(const pts_typed_array *array, double value, double from) {
  for (size_t i = search_start(from, array->length); i < array->length; i++) {
    double element = pts_typed_array_as_number(array, i);
    if (element == value || (element != element && value != value)) {
      return true;
    }
  }
  return false;
}

/* The elements' strings, each made by to_string, joined with separator. */
static pts_string *joined(const pts_typed_array *array, pts_string *separator, pts_string *(*to_string)(double)) {
  pts_string *result = pts_string_from_cstr("");
  for (size_t i = 0; i < array->length; i++) {
    if (i > 0) {
      result = pts_string_concat(result, separator);
    }
    result = pts_string_concat(result, to_string(pts_typed_array_as_number(array, i)));
  }
  return result;
}

pts_string *pts_typed_array_join(const pts_typed_array *array, pts_string *separator) {
  return joined(array, separator == NULL ? pts_string_from_cstr(",") : separator, pts_number_to_string);
}

pts_string *pts_typed_array_to_locale_string(const pts_typed_array *array) {
  return joined(array, pts_string_from_cstr(","), pts_number_to_locale_string);
}

pts_array *pts_typed_array_to_array(const pts_typed_array *array) {
  size_t length = array->length;
  double *elements = malloc((length > 0 ? length : 1) * sizeof *elements);
  if (elements == NULL) {
    pts_panic("RangeError", "Out of memory copying a %s of %zu elements", array->type->name, length);
  }
  for (size_t i = 0; i < length; i++) {
    elements[i] = pts_typed_array_as_number(array, i);
  }
  pts_array *numbers = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, length);
  free(elements);
  return numbers;
}
