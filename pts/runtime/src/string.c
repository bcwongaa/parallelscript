#include "pts.h"

#include <stdint.h>

const pts_type pts_string_type = {PTS_KIND_STRING, "string"};

/* JS caps string length; V8's limit is 2^29 - 24 UTF-16 units. Stay well inside uint32_t. */
#define PTS_STRING_MAX_BYTES (UINT32_C(1) << 30)

static pts_string *alloc_string(size_t byte_len) {
  if (byte_len > PTS_STRING_MAX_BYTES) {
    pts_panic("RangeError", "Invalid string length");
  }
  /* Pointer-free payload (the type pointer is static), so bdwgc never scans it. */
  pts_string *string = GC_MALLOC_ATOMIC(sizeof(pts_string) + byte_len + 1);
  if (string == NULL) {
    pts_panic("RangeError", "Out of memory allocating a string of %zu bytes", byte_len);
  }
  string->type = &pts_string_type;
  string->byte_len = (uint32_t)byte_len;
  string->bytes[byte_len] = '\0';
  return string;
}

/* Fills in flags and utf16_len from the bytes, which must be valid UTF-8. */
static void measure_string(pts_string *string) {
  uint32_t utf16_len = 0;
  bool ascii = true;
  for (uint32_t i = 0; i < string->byte_len; i++) {
    unsigned char byte = (unsigned char)string->bytes[i];
    if (byte < 0x80) {
      utf16_len++;
      continue;
    }
    ascii = false;
    if ((byte & 0xC0) == 0x80) {
      continue; /* continuation byte */
    }
    /* A 4-byte sequence is outside the BMP: a surrogate pair in UTF-16. */
    utf16_len += byte >= 0xF0 ? 2 : 1;
  }
  string->flags = ascii ? PTS_STRING_ASCII : 0;
  string->utf16_len = utf16_len;
}

pts_string *pts_string_from_utf8(const char *bytes, size_t len) {
  pts_string *string = alloc_string(len);
  memcpy(string->bytes, bytes, len);
  measure_string(string);
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
  size_t byte_len = (size_t)left->byte_len + right->byte_len;
  pts_string *string = alloc_string(byte_len);
  memcpy(string->bytes, left->bytes, left->byte_len);
  memcpy(string->bytes + left->byte_len, right->bytes, right->byte_len);
  string->flags = left->flags & right->flags & PTS_STRING_ASCII;
  string->utf16_len = left->utf16_len + right->utf16_len;
  return string;
}

bool pts_string_equals(const pts_string *left, const pts_string *right) {
  return left == right ||
         (left->byte_len == right->byte_len && memcmp(left->bytes, right->bytes, left->byte_len) == 0);
}
