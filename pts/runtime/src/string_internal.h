/*
 * What the string sources (string.c, string_methods.c, string_case.c) share; not
 * part of the runtime's API. Strings are WTF-8 (D-014): UTF-8, except that a lone
 * surrogate is its 3-byte generalised encoding.
 */
#ifndef PTS_STRING_INTERNAL_H
#define PTS_STRING_INTERNAL_H

#include "pts.h"

/* JS caps string length; V8's limit is 2^29 - 24 UTF-16 units. Stay well inside uint32_t. */
#define PTS_STRING_MAX_BYTES (UINT32_C(1) << 30)

/* A string of byte_len bytes, NUL-terminated. The caller writes the bytes, then sets flags and utf16_len (or calls pts_string_measure). */
pts_string *pts_string_alloc(size_t byte_len);

/* Sets flags and utf16_len from the bytes. */
void pts_string_measure(pts_string *string);

_Noreturn void pts_throw_invalid_string_length(void);

/* Reads the UTF-16 code unit at *pos, advancing *pos; *pending holds the low half of a pair between calls (start it at 0). */
unsigned pts_string_next_unit(const pts_string *string, uint32_t *pos, unsigned *pending);

/* ---------------------------------------------------------------- WTF-8 */

enum { PTS_SURROGATE_BYTES = 3 };

static inline bool pts_utf8_is_continuation(unsigned char byte) { return (byte & 0xC0) == 0x80; }

/* Decodes the character at bytes (a surrogate decodes to its own value) and stores its byte length. */
static inline unsigned pts_utf8_decode(const char *bytes, unsigned *length) {
  const unsigned char *b = (const unsigned char *)bytes;
  if (b[0] < 0x80) {
    *length = 1;
    return b[0];
  }
  if (b[0] < 0xE0) {
    *length = 2;
    return ((b[0] & 0x1Fu) << 6) | (b[1] & 0x3Fu);
  }
  if (b[0] < 0xF0) {
    *length = 3;
    return ((b[0] & 0x0Fu) << 12) | ((b[1] & 0x3Fu) << 6) | (b[2] & 0x3Fu);
  }
  *length = 4;
  return ((b[0] & 0x07u) << 18) | ((b[1] & 0x3Fu) << 12) | ((b[2] & 0x3Fu) << 6) | (b[3] & 0x3Fu);
}

static inline unsigned pts_utf8_length(unsigned code_point) {
  return code_point < 0x80 ? 1 : code_point < 0x800 ? 2 : code_point < 0x10000 ? 3 : 4;
}

/* Writes code_point (a surrogate as its 3-byte form) and returns the byte count. */
static inline unsigned pts_utf8_encode(unsigned code_point, char *out) {
  if (code_point < 0x80) {
    out[0] = (char)code_point;
    return 1;
  }
  if (code_point < 0x800) {
    out[0] = (char)(0xC0 | (code_point >> 6));
    out[1] = (char)(0x80 | (code_point & 0x3F));
    return 2;
  }
  if (code_point < 0x10000) {
    out[0] = (char)(0xE0 | (code_point >> 12));
    out[1] = (char)(0x80 | ((code_point >> 6) & 0x3F));
    out[2] = (char)(0x80 | (code_point & 0x3F));
    return 3;
  }
  out[0] = (char)(0xF0 | (code_point >> 18));
  out[1] = (char)(0x80 | ((code_point >> 12) & 0x3F));
  out[2] = (char)(0x80 | ((code_point >> 6) & 0x3F));
  out[3] = (char)(0x80 | (code_point & 0x3F));
  return 4;
}

static inline unsigned pts_high_surrogate(unsigned code_point) { return 0xD800u + ((code_point - 0x10000u) >> 10); }
static inline unsigned pts_low_surrogate(unsigned code_point) { return 0xDC00u + ((code_point - 0x10000u) & 0x3FFu); }

/* Whether the bytes at `at` are a lone high (0xED 0xA0-0xAF) or low (0xED 0xB0-0xBF) surrogate. */
static inline bool pts_wtf8_surrogate_at(const char *bytes, size_t len, size_t at, unsigned char second_min,
                                         unsigned char second_max) {
  if (at + PTS_SURROGATE_BYTES > len) {
    return false;
  }
  unsigned char lead = (unsigned char)bytes[at], second = (unsigned char)bytes[at + 1];
  return lead == 0xED && second >= second_min && second <= second_max;
}

static inline bool pts_wtf8_high_surrogate_at(const char *bytes, size_t len, size_t at) {
  return pts_wtf8_surrogate_at(bytes, len, at, 0xA0, 0xAF);
}

static inline bool pts_wtf8_low_surrogate_at(const char *bytes, size_t len, size_t at) {
  return pts_wtf8_surrogate_at(bytes, len, at, 0xB0, 0xBF);
}

static inline bool pts_string_ends_with_high_surrogate(const pts_string *string) {
  return string->byte_len >= PTS_SURROGATE_BYTES &&
         pts_wtf8_high_surrogate_at(string->bytes, string->byte_len, string->byte_len - PTS_SURROGATE_BYTES);
}

static inline bool pts_string_starts_with_low_surrogate(const pts_string *string) {
  return pts_wtf8_low_surrogate_at(string->bytes, string->byte_len, 0);
}

#endif /* PTS_STRING_INTERNAL_H */
