/*
 * encodeURI, encodeURIComponent, decodeURI and decodeURIComponent (ECMAScript 19.2.6):
 * percent-encoding of a string's UTF-8 bytes. A lone surrogate to encode, or an escape
 * that isn't one of valid UTF-8, throws `URIError: URI malformed`, as in V8.
 */
#include "pts.h"

#include <stdlib.h>
#include <string.h>

/* The characters encodeURIComponent leaves as they are, besides letters and digits. */
static const char MARKS[] = "-_.!~*'()";
/* The characters encodeURI also leaves, and decodeURI leaves escaped. */
static const char RESERVED[] = ";/?:@&=+$,#";

static const char HEX[] = "0123456789ABCDEF";

typedef struct bytes {
  char *data;
  size_t len, capacity;
} bytes;

static void put(bytes *b, char c) {
  if (b->len == b->capacity) {
    b->capacity = b->capacity ? b->capacity * 2 : 64;
    b->data = realloc(b->data, b->capacity);
    if (b->data == NULL) {
      abort();
    }
  }
  b->data[b->len++] = c;
}

static pts_string *finish(bytes *b) {
  pts_string *string = pts_string_from_utf8(b->data, b->len);
  free(b->data);
  return string;
}

_Noreturn static void malformed(bytes *b) {
  free(b->data);
  pts_throw_new(&pts_type_builtin_URIError, "URI malformed");
}

static bool is_one_of(unsigned char c, const char *set) { return c != 0 && strchr(set, c) != NULL; }

static bool is_alphanumeric(unsigned char c) { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'); }

pts_string *pts_uri_encode(pts_string *string, bool component) {
  bytes out = {0};
  const unsigned char *s = (const unsigned char *)string->bytes;
  for (size_t i = 0; i < string->byte_len; i++) {
    unsigned char c = s[i];
    if (c < 0x80 && (is_alphanumeric(c) || is_one_of(c, MARKS) || (!component && is_one_of(c, RESERVED)))) {
      put(&out, (char)c);
      continue;
    }
    if (c == 0xED && i + 1 < string->byte_len && s[i + 1] >= 0xA0) {
      malformed(&out); /* a lone surrogate, which WTF-8 holds as ED A0..BF xx (D-014) */
    }
    put(&out, '%');
    put(&out, HEX[c >> 4]);
    put(&out, HEX[c & 0xF]);
  }
  return finish(&out);
}

static int hex_digit(unsigned char c) {
  if (c >= '0' && c <= '9') {
    return c - '0';
  }
  if (c >= 'a' && c <= 'f') {
    return c - 'a' + 10;
  }
  if (c >= 'A' && c <= 'F') {
    return c - 'A' + 10;
  }
  return -1;
}

/* The byte the escape at s[i] (a '%') stands for, or -1 if it isn't one. */
static int escaped_byte(const unsigned char *s, size_t len, size_t i) {
  if (i + 2 >= len || s[i] != '%') {
    return -1;
  }
  int high = hex_digit(s[i + 1]), low = hex_digit(s[i + 2]);
  return high < 0 || low < 0 ? -1 : high * 16 + low;
}

/* The number of bytes of the UTF-8 sequence that starts with lead, or 0 if none does. */
static int sequence_length(int lead) {
  if (lead >= 0xC2 && lead <= 0xDF) {
    return 2;
  }
  if (lead >= 0xE0 && lead <= 0xEF) {
    return 3;
  }
  if (lead >= 0xF0 && lead <= 0xF4) {
    return 4;
  }
  return 0;
}

/* Whether second may follow lead in UTF-8: no overlong forms, surrogates or code points past U+10FFFF. */
static bool second_fits(int lead, int second) {
  switch (lead) {
  case 0xE0:
    return second >= 0xA0 && second <= 0xBF;
  case 0xED:
    return second >= 0x80 && second <= 0x9F;
  case 0xF0:
    return second >= 0x90 && second <= 0xBF;
  case 0xF4:
    return second >= 0x80 && second <= 0x8F;
  }
  return second >= 0x80 && second <= 0xBF;
}

pts_string *pts_uri_decode(pts_string *string, bool component) {
  bytes out = {0};
  const unsigned char *s = (const unsigned char *)string->bytes;
  size_t len = string->byte_len;
  for (size_t i = 0; i < len;) {
    if (s[i] != '%') {
      put(&out, (char)s[i++]);
      continue;
    }
    int lead = escaped_byte(s, len, i);
    if (lead < 0) {
      malformed(&out);
    }
    if (lead < 0x80) {
      if (!component && is_one_of((unsigned char)lead, RESERVED)) {
        put(&out, (char)s[i]); /* decodeURI keeps the escape of a reserved character */
        put(&out, (char)s[i + 1]);
        put(&out, (char)s[i + 2]);
      } else {
        put(&out, (char)lead);
      }
      i += 3;
      continue;
    }
    int n = sequence_length(lead);
    if (n == 0) {
      malformed(&out);
    }
    unsigned char sequence[4] = {(unsigned char)lead};
    for (int k = 1; k < n; k++) {
      int next = escaped_byte(s, len, i + 3 * (size_t)k);
      if (next < 0 || (k == 1 ? !second_fits(lead, next) : next < 0x80 || next > 0xBF)) {
        malformed(&out);
      }
      sequence[k] = (unsigned char)next;
    }
    for (int k = 0; k < n; k++) {
      put(&out, (char)sequence[k]);
    }
    i += 3 * (size_t)n;
  }
  return finish(&out);
}
