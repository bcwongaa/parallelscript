#include "string_internal.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>

const pts_type pts_string_type = {.kind = PTS_KIND_STRING, .gc_descr = PTS_GC_NO_POINTERS, .name = "string"};

_Noreturn void pts_throw_invalid_string_length(void) { pts_throw_new(&pts_type_builtin_RangeError, "Invalid string length"); }

pts_string *pts_string_alloc(size_t byte_len) {
  if (byte_len > PTS_STRING_MAX_BYTES) {
    pts_throw_invalid_string_length();
  }
  /* Pointer-free for the collector (the type is static and bytes points into the string itself), so it is never scanned. */
  pts_string *string = GC_MALLOC_ATOMIC(sizeof(pts_string) + byte_len + 1);
  if (string == NULL) {
    pts_panic("RangeError", "Out of memory allocating a string of %zu bytes", byte_len);
  }
  string->type = &pts_string_type;
  string->flags = 0;
  string->byte_len = (uint32_t)byte_len;
  string->bytes = (char *)(string + 1);
  string->bytes[byte_len] = '\0';
  return string;
}

/* Concatenations at least this long go to an append buffer, with as much room again for what follows. */
enum { BUFFERED_MIN_BYTES = 64, BUFFER_GROWTH = 2 };

static pts_string_buffer *buffer_of(const pts_string *string) {
  return (pts_string_buffer *)(string->bytes - offsetof(pts_string_buffer, data));
}

/* buffered_string is a new string header for the first byte_len bytes of buffer. */
static pts_string *buffered_string(pts_string_buffer *buffer, size_t byte_len, uint32_t utf16_len, uint32_t flags) {
  /* Scanned, unlike other strings: bytes keeps the buffer alive. */
  pts_string *string = GC_MALLOC(sizeof(pts_string));
  if (string == NULL) {
    pts_panic("RangeError", "Out of memory allocating a string of %zu bytes", byte_len);
  }
  string->type = &pts_string_type;
  string->flags = flags | PTS_STRING_BUFFERED;
  string->byte_len = (uint32_t)byte_len;
  string->utf16_len = utf16_len;
  string->bytes = buffer->data;
  return string;
}

/* new_buffer is an empty append buffer with room for capacity bytes, at most the longest string's. */
static pts_string_buffer *new_buffer(size_t capacity) {
  if (capacity > PTS_STRING_MAX_BYTES) {
    capacity = PTS_STRING_MAX_BYTES;
  }
  pts_string_buffer *buffer = GC_MALLOC_ATOMIC(sizeof(pts_string_buffer) + capacity);
  if (buffer == NULL) {
    pts_panic("RangeError", "Out of memory allocating a string of %zu bytes", capacity);
  }
  buffer->capacity = capacity;
  buffer->used = 0;
  return buffer;
}

/* appended is left + right made by claiming the bytes after left in its append buffer,
 * or NULL if left doesn't end where the buffer's claimed bytes do or the buffer is full. */
static pts_string *appended(const pts_string *left, const pts_string *right) {
  if (!(left->flags & PTS_STRING_BUFFERED)) {
    return NULL;
  }
  pts_string_buffer *buffer = buffer_of(left);
  size_t end = left->byte_len, byte_len = end + right->byte_len;
  if (byte_len > buffer->capacity ||
      !__atomic_compare_exchange_n(&buffer->used, &end, byte_len, false, __ATOMIC_ACQ_REL, __ATOMIC_RELAXED)) {
    return NULL;
  }
  memcpy(buffer->data + left->byte_len, right->bytes, right->byte_len); /* right may be in this buffer, but before `end` */
  return buffered_string(buffer, byte_len, left->utf16_len + right->utf16_len, left->flags & right->flags & PTS_STRING_ASCII);
}

void pts_string_measure(pts_string *string) {
  uint32_t utf16_len = 0;
  bool ascii = true;
  for (uint32_t i = 0; i < string->byte_len; i++) {
    unsigned char byte = (unsigned char)string->bytes[i];
    if (byte < 0x80) {
      utf16_len++;
      continue;
    }
    ascii = false;
    if (pts_utf8_is_continuation(byte)) {
      continue;
    }
    /* A 4-byte sequence is outside the BMP: a surrogate pair in UTF-16. */
    utf16_len += byte >= 0xF0 ? 2 : 1;
  }
  string->flags = ascii ? PTS_STRING_ASCII : 0;
  string->utf16_len = utf16_len;
}

pts_string *pts_string_from_utf8(const char *bytes, size_t len) {
  pts_string *string = pts_string_alloc(len);
  memcpy(string->bytes, bytes, len);
  pts_string_measure(string);
  return string;
}

pts_string *pts_string_from_cstr(const char *cstr) { return pts_string_from_utf8(cstr, strlen(cstr)); }

pts_string *pts_string_concat(pts_string *left, pts_string *right) {
  if (left->byte_len == 0) {
    return right;
  }
  if (right->byte_len == 0) {
    return left;
  }
  /* A high surrogate meeting a low one becomes the 4-byte encoding of their code point, as in JS. */
  bool join = pts_string_ends_with_high_surrogate(left) && pts_string_starts_with_low_surrogate(right);
  pts_string *string;
  if (!join && (string = appended(left, right)) != NULL) {
    return string;
  }
  size_t left_keep = left->byte_len - (join ? PTS_SURROGATE_BYTES : 0);
  size_t right_skip = join ? PTS_SURROGATE_BYTES : 0;
  size_t byte_len = left_keep + (join ? 4 : 0) + (right->byte_len - right_skip);
  if (byte_len > PTS_STRING_MAX_BYTES) {
    pts_throw_invalid_string_length();
  }
  uint32_t buffered = 0;
  if (byte_len >= BUFFERED_MIN_BYTES) {
    pts_string_buffer *buffer = new_buffer(BUFFER_GROWTH * byte_len);
    buffer->used = byte_len;
    string = buffered_string(buffer, byte_len, 0, 0);
    buffered = PTS_STRING_BUFFERED;
  } else {
    string = pts_string_alloc(byte_len);
  }
  char *out = string->bytes;
  memcpy(out, left->bytes, left_keep);
  out += left_keep;
  if (join) {
    unsigned length;
    unsigned high = pts_utf8_decode(left->bytes + left_keep, &length), low = pts_utf8_decode(right->bytes, &length);
    out += pts_utf8_encode(0x10000u + ((high - 0xD800u) << 10) + (low - 0xDC00u), out);
  }
  memcpy(out, right->bytes + right_skip, right->byte_len - right_skip);
  string->flags = (left->flags & right->flags & PTS_STRING_ASCII) | buffered;
  string->utf16_len = left->utf16_len + right->utf16_len;
  return string;
}

unsigned pts_string_next_unit(const pts_string *string, uint32_t *pos, unsigned *pending) {
  if (*pending != 0) {
    unsigned unit = *pending;
    *pending = 0;
    return unit;
  }
  unsigned length;
  unsigned code_point = pts_utf8_decode(string->bytes + *pos, &length);
  *pos += length;
  if (code_point < 0x10000) {
    return code_point;
  }
  *pending = pts_low_surrogate(code_point);
  return pts_high_surrogate(code_point);
}

int pts_string_compare(const pts_string *left, const pts_string *right) {
  if (left->flags & right->flags & PTS_STRING_ASCII) {
    int order = memcmp(left->bytes, right->bytes, left->byte_len < right->byte_len ? left->byte_len : right->byte_len);
    if (order != 0) {
      return order;
    }
    return left->byte_len < right->byte_len ? -1 : left->byte_len > right->byte_len;
  }
  uint32_t left_pos = 0, right_pos = 0;
  unsigned left_pending = 0, right_pending = 0;
  for (;;) {
    bool left_done = left_pos >= left->byte_len && left_pending == 0;
    bool right_done = right_pos >= right->byte_len && right_pending == 0;
    if (left_done || right_done) {
      return right_done - left_done;
    }
    unsigned a = pts_string_next_unit(left, &left_pos, &left_pending);
    unsigned b = pts_string_next_unit(right, &right_pos, &right_pending);
    if (a != b) {
      return a < b ? -1 : 1;
    }
  }
}

pts_string *pts_string_from_i64(int64_t value) {
  char buf[24];
  int len = snprintf(buf, sizeof buf, "%" PRId64, value);
  return pts_string_from_utf8(buf, (size_t)len);
}

pts_string *pts_string_from_u64(uint64_t value) {
  char buf[24];
  int len = snprintf(buf, sizeof buf, "%" PRIu64, value);
  return pts_string_from_utf8(buf, (size_t)len);
}

size_t pts_string_write_for_output(const pts_string *string, void *stream) {
  FILE *out = stream;
  if (string->flags & PTS_STRING_ASCII) {
    return fwrite(string->bytes, 1, string->byte_len, out);
  }
  size_t written = 0, start = 0;
  for (size_t i = 0; i < string->byte_len;) {
    if (pts_wtf8_surrogate_at(string->bytes, string->byte_len, i, 0xA0, 0xBF)) {
      written += fwrite(string->bytes + start, 1, i - start, out);
      written += fwrite("\xEF\xBF\xBD", 1, 3, out); /* U+FFFD */
      i += PTS_SURROGATE_BYTES;
      start = i;
    } else {
      i++;
    }
  }
  return written + fwrite(string->bytes + start, 1, string->byte_len - start, out);
}

bool pts_string_equals(const pts_string *left, const pts_string *right) {
  return left == right ||
         (left->byte_len == right->byte_len && memcmp(left->bytes, right->bytes, left->byte_len) == 0);
}

PTS_STATIC_STRING(undefined_word, PTS_STRING_ASCII, 9, 9, "undefined");
PTS_STATIC_STRING(null_word, PTS_STRING_ASCII, 4, 4, "null");
PTS_STATIC_STRING(true_word, PTS_STRING_ASCII, 4, 4, "true");
PTS_STATIC_STRING(false_word, PTS_STRING_ASCII, 5, 5, "false");

pts_string *pts_value_to_string(pts_value value) {
  if (pts_value_is_number(value)) {
    return pts_number_to_string(pts_value_as_number(value));
  }
  if (pts_value_is_object(value)) {
    pts_object *object = pts_value_as_object(value);
    switch (object->type->kind) {
    case PTS_KIND_STRING:
      return (pts_string *)object;
    case PTS_KIND_INT64:
      return pts_string_from_i64(((pts_boxed_int64 *)object)->value);
    case PTS_KIND_UINT64:
      return pts_string_from_u64(((pts_boxed_uint64 *)object)->value);
    case PTS_KIND_OBJECT:
    case PTS_KIND_ARRAY:
    case PTS_KIND_TUPLE:
    case PTS_KIND_MAP:
    case PTS_KIND_SET:
    case PTS_KIND_RECORD:
    case PTS_KIND_DATE:
      return pts_object_to_string(object);
    case PTS_KIND_FUNCTION:
      pts_panic("TypeError", "converting a function to a string is not supported yet");
    default:
      break;
    }
    pts_panic("TypeError", "converting a %s value to a string is not supported yet", object->type->name);
  }
  switch (value) {
  case PTS_VALUE_UNDEFINED:
    return undefined_word;
  case PTS_VALUE_NULL:
    return null_word;
  case PTS_VALUE_TRUE:
    return true_word;
  case PTS_VALUE_FALSE:
    return false_word;
  default:
    pts_panic("Error", "ToString got a malformed value 0x%llx", (unsigned long long)value);
  }
}

bool pts_value_truthy(pts_value value) {
  if (pts_value_is_number(value)) {
    return pts_number_truthy(pts_value_as_number(value));
  }
  if (pts_value_is_object(value)) {
    pts_object *object = pts_value_as_object(value);
    /* A boxed 64-bit integer is beyond 2^53, so never 0. */
    return object->type->kind != PTS_KIND_STRING || ((pts_string *)object)->byte_len != 0;
  }
  return value == PTS_VALUE_TRUE;
}

void pts_throw_non_null(pts_value value) {
  pts_throw_new(&pts_type_builtin_TypeError, "Non-null assertion on %s", value == PTS_VALUE_NULL ? "null" : "undefined");
}

void pts_throw_not_primitive(pts_value value, const char *type_name) { pts_throw_type_mismatch(value, type_name); }

double pts_value_to_number_checked(pts_value value) {
  if (pts_value_is_number(value)) {
    return pts_value_as_number(value);
  }
  if (pts_value_is_object(value)) {
    const pts_object *object = pts_value_as_object(value);
    if (object->type->kind == PTS_KIND_INT64) {
      return (double)((const pts_boxed_int64 *)object)->value;
    }
    if (object->type->kind == PTS_KIND_UINT64) {
      return (double)((const pts_boxed_uint64 *)object)->value;
    }
  }
  pts_throw_not_primitive(value, "number");
}

void pts_throw_not_instance(const pts_type *type) {
  pts_throw_new(&pts_type_builtin_TypeError, "The value narrowed to %s is not an instance of it", type->name);
}

bool pts_value_strict_equals(pts_value a, pts_value b) {
  if (pts_value_is_number(a) || pts_value_is_number(b)) {
    return pts_value_is_number(a) && pts_value_is_number(b) && pts_value_as_number(a) == pts_value_as_number(b);
  }
  if (a == b) {
    return true;
  }
  if (!pts_value_is_object(a) || !pts_value_is_object(b)) {
    return false;
  }
  const pts_object *x = pts_value_as_object(a), *y = pts_value_as_object(b);
  if (x->type->kind != y->type->kind) {
    return false;
  }
  switch (x->type->kind) {
  case PTS_KIND_STRING:
    return pts_string_equals((const pts_string *)x, (const pts_string *)y);
  case PTS_KIND_INT64:
    return ((const pts_boxed_int64 *)x)->value == ((const pts_boxed_int64 *)y)->value;
  case PTS_KIND_UINT64:
    return ((const pts_boxed_uint64 *)x)->value == ((const pts_boxed_uint64 *)y)->value;
  default:
    return false;
  }
}

/* What == compares a value as: JS's types, with 64-bit integers among the numbers. */
typedef enum loose_kind { LOOSE_NULLISH, LOOSE_BOOLEAN, LOOSE_NUMBER, LOOSE_STRING, LOOSE_OBJECT } loose_kind;

static loose_kind loose_kind_of(pts_value value) {
  if (pts_value_is_number(value)) {
    return LOOSE_NUMBER;
  }
  if (pts_value_is_nullish(value)) {
    return LOOSE_NULLISH;
  }
  if (value == PTS_VALUE_TRUE || value == PTS_VALUE_FALSE) {
    return LOOSE_BOOLEAN;
  }
  switch (pts_value_as_object(value)->type->kind) {
  case PTS_KIND_STRING:
    return LOOSE_STRING;
  case PTS_KIND_INT64:
  case PTS_KIND_UINT64:
    return LOOSE_NUMBER;
  default:
    return LOOSE_OBJECT;
  }
}

/* as_number is JS's ToNumber of a number or string value. */
static double as_number(pts_value value) {
  if (pts_value_is_number(value)) {
    return pts_value_as_number(value);
  }
  const pts_object *object = pts_value_as_object(value);
  switch (object->type->kind) {
  case PTS_KIND_STRING:
    return pts_string_to_number((const pts_string *)object);
  case PTS_KIND_INT64:
    return (double)((const pts_boxed_int64 *)object)->value;
  default:
    return (double)((const pts_boxed_uint64 *)object)->value;
  }
}

bool pts_value_loose_equals(pts_value a, pts_value b) {
  for (;;) { /* JS's IsLooselyEqual, which converts one side and compares again */
    loose_kind x = loose_kind_of(a), y = loose_kind_of(b);
    if (x == y) {
      return x == LOOSE_NULLISH || pts_value_strict_equals(a, b);
    }
    if (x == LOOSE_NULLISH || y == LOOSE_NULLISH) {
      return false;
    }
    if (x == LOOSE_BOOLEAN || y == LOOSE_BOOLEAN) {
      pts_value *boolean = x == LOOSE_BOOLEAN ? &a : &b;
      *boolean = pts_value_from_number(*boolean == PTS_VALUE_TRUE ? 1 : 0);
      continue;
    }
    if (x == LOOSE_OBJECT || y == LOOSE_OBJECT) {
      pts_value *object = x == LOOSE_OBJECT ? &a : &b;
      const pts_object *converted = pts_value_as_object(*object);
      *object = converted->type->kind == PTS_KIND_OBJECT ? pts_value_to_primitive(*object, false) /* valueOf first */
                                                         : pts_value_from_string(pts_object_to_string(converted));
      continue;
    }
    return as_number(a) == as_number(b); /* a number and a string */
  }
}

PTS_STATIC_STRING(number_word, PTS_STRING_ASCII, 6, 6, "number");
PTS_STATIC_STRING(string_word, PTS_STRING_ASCII, 6, 6, "string");
PTS_STATIC_STRING(boolean_word, PTS_STRING_ASCII, 7, 7, "boolean");
PTS_STATIC_STRING(object_word, PTS_STRING_ASCII, 6, 6, "object");
PTS_STATIC_STRING(function_word, PTS_STRING_ASCII, 8, 8, "function");

pts_string *pts_value_typeof(pts_value value) {
  if (pts_value_is_number(value)) {
    return number_word;
  }
  if (pts_value_is_object(value)) {
    switch (pts_value_as_object(value)->type->kind) {
    case PTS_KIND_STRING:
      return string_word;
    case PTS_KIND_INT64:
    case PTS_KIND_UINT64:
      return number_word;
    case PTS_KIND_FUNCTION:
      return function_word;
    default:
      break;
    }
    return object_word;
  }
  switch (value) {
  case PTS_VALUE_UNDEFINED:
    return undefined_word;
  case PTS_VALUE_TRUE:
  case PTS_VALUE_FALSE:
    return boolean_word;
  default:
    return object_word; /* null */
  }
}

/* `o.constructor.name` (D-089): the name of o's class, or of the wrapper of a primitive. */
pts_string *pts_value_constructor_name(pts_value value) {
  const char *name = "Boolean";
  if (pts_value_is_number(value)) {
    name = "Number";
  } else if (pts_value_is_object(value)) {
    const pts_type *type = pts_value_as_object(value)->type;
    switch (type->kind) {
    case PTS_KIND_STRING:
      name = "String";
      break;
    case PTS_KIND_INT64:
    case PTS_KIND_UINT64:
      name = "Number";
      break;
    case PTS_KIND_ARRAY:
    case PTS_KIND_TUPLE:
      name = "Array";
      break;
    case PTS_KIND_FUNCTION:
      name = "Function";
      break;
    case PTS_KIND_MAP:
      name = "Map";
      break;
    case PTS_KIND_SET:
      name = "Set";
      break;
    case PTS_KIND_RECORD:
      name = "Object";
      break;
    case PTS_KIND_DATE:
      name = "Date";
      break;
    default:
      name = type->name; /* a class's, or "Object" for an object type's shape */
      break;
    }
  } else if (value == PTS_VALUE_NULL || value == PTS_VALUE_UNDEFINED) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot read properties of %s (reading 'constructor')", value == PTS_VALUE_NULL ? "null" : "undefined");
  }
  return pts_string_from_cstr(name);
}
