#include "pts.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>

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

/*
 * A lone surrogate is stored as its 3-byte generalised UTF-8 encoding (WTF-8):
 * 0xED, then 0xA0-0xAF for a high surrogate or 0xB0-0xBF for a low one.
 */
enum { SURROGATE_BYTES = 3 };

static bool is_surrogate_at(const char *bytes, size_t len, size_t at, unsigned char second_min, unsigned char second_max) {
  if (at + SURROGATE_BYTES > len) {
    return false;
  }
  unsigned char lead = (unsigned char)bytes[at], second = (unsigned char)bytes[at + 1];
  return lead == 0xED && second >= second_min && second <= second_max;
}

static unsigned surrogate_value(const char *bytes) {
  return 0xD000u | (((unsigned)bytes[1] & 0x3Fu) << 6) | ((unsigned)bytes[2] & 0x3Fu);
}

pts_string *pts_string_concat(pts_string *left, pts_string *right) {
  if (left->byte_len == 0) {
    return right;
  }
  if (right->byte_len == 0) {
    return left;
  }
  /* A high surrogate meeting a low one becomes the 4-byte encoding of their code point, as in JS. */
  bool join = left->byte_len >= SURROGATE_BYTES &&
              is_surrogate_at(left->bytes, left->byte_len, left->byte_len - SURROGATE_BYTES, 0xA0, 0xAF) &&
              is_surrogate_at(right->bytes, right->byte_len, 0, 0xB0, 0xBF);
  size_t left_keep = left->byte_len - (join ? SURROGATE_BYTES : 0);
  size_t right_skip = join ? SURROGATE_BYTES : 0;
  size_t byte_len = left_keep + (join ? 4 : 0) + (right->byte_len - right_skip);
  pts_string *string = alloc_string(byte_len);
  char *out = string->bytes;
  memcpy(out, left->bytes, left_keep);
  out += left_keep;
  if (join) {
    unsigned high = surrogate_value(left->bytes + left_keep), low = surrogate_value(right->bytes);
    unsigned code_point = 0x10000u + ((high - 0xD800u) << 10) + (low - 0xDC00u);
    *out++ = (char)(0xF0 | (code_point >> 18));
    *out++ = (char)(0x80 | ((code_point >> 12) & 0x3F));
    *out++ = (char)(0x80 | ((code_point >> 6) & 0x3F));
    *out++ = (char)(0x80 | (code_point & 0x3F));
  }
  memcpy(out, right->bytes + right_skip, right->byte_len - right_skip);
  string->flags = left->flags & right->flags & PTS_STRING_ASCII;
  string->utf16_len = left->utf16_len + right->utf16_len;
  return string;
}

/* Reads the UTF-16 code unit at *pos (with *pending holding the low half of a pair), advancing *pos. */
static unsigned next_utf16_unit(const pts_string *string, uint32_t *pos, unsigned *pending) {
  if (*pending != 0) {
    unsigned unit = *pending;
    *pending = 0;
    return unit;
  }
  const unsigned char *b = (const unsigned char *)string->bytes + *pos;
  if (b[0] < 0x80) {
    *pos += 1;
    return b[0];
  }
  if (b[0] < 0xE0) {
    *pos += 2;
    return ((b[0] & 0x1Fu) << 6) | (b[1] & 0x3Fu);
  }
  if (b[0] < 0xF0) {
    *pos += 3;
    return ((b[0] & 0x0Fu) << 12) | ((b[1] & 0x3Fu) << 6) | (b[2] & 0x3Fu);
  }
  *pos += 4;
  unsigned code_point = ((b[0] & 0x07u) << 18) | ((b[1] & 0x3Fu) << 12) | ((b[2] & 0x3Fu) << 6) | (b[3] & 0x3Fu);
  code_point -= 0x10000u;
  *pending = 0xDC00u + (code_point & 0x3FFu);
  return 0xD800u + (code_point >> 10);
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
    unsigned a = next_utf16_unit(left, &left_pos, &left_pending);
    unsigned b = next_utf16_unit(right, &right_pos, &right_pending);
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
    if (is_surrogate_at(string->bytes, string->byte_len, i, 0xA0, 0xBF)) {
      written += fwrite(string->bytes + start, 1, i - start, out);
      written += fwrite("\xEF\xBF\xBD", 1, 3, out); /* U+FFFD */
      i += SURROGATE_BYTES;
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
