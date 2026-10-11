/*
 * String.prototype methods (docs/SPEC.md §9.6). Indices count UTF-16 code units, as
 * in JS. An ASCII string's units are its bytes, so it takes O(1) paths; a non-ASCII
 * string is scanned from the start to find an index. Index arguments arrive as
 * doubles and go through ToIntegerOrInfinity, so the compiler passes NaN for a
 * left-out index that means 0 and Infinity for one that means the length.
 */
#include "string_internal.h"

#include <inttypes.h>

PTS_STATIC_STRING(empty_string, PTS_STRING_ASCII, 0, 0, "");

/* The one-character ASCII strings, so that s[i] on ASCII text doesn't allocate. */
typedef struct ascii_char_string {
  const pts_type *type;
  uint32_t flags, byte_len, utf16_len;
  char bytes[2];
} ascii_char_string;

#define ASCII_CHAR(c) {&pts_string_type, PTS_STRING_ASCII, 1, 1, {(char)(c), 0}}
#define ASCII_CHARS_8(c)                                                                          \
  ASCII_CHAR(c), ASCII_CHAR(c + 1), ASCII_CHAR(c + 2), ASCII_CHAR(c + 3), ASCII_CHAR(c + 4),      \
      ASCII_CHAR(c + 5), ASCII_CHAR(c + 6), ASCII_CHAR(c + 7)
#define ASCII_CHARS_32(c) ASCII_CHARS_8(c), ASCII_CHARS_8(c + 8), ASCII_CHARS_8(c + 16), ASCII_CHARS_8(c + 24)

static const ascii_char_string ascii_chars[128] = {
    ASCII_CHARS_32(0), ASCII_CHARS_32(32), ASCII_CHARS_32(64), ASCII_CHARS_32(96),
};

/* ---------------------------------------------------------------- arguments */

/* ECMAScript ToIntegerOrInfinity: NaN is 0, everything else truncates toward zero (-0 becomes 0). */
static double to_integer_or_infinity(double d) {
  if (d != d) {
    return 0;
  }
  return __builtin_trunc(d) + 0.0;
}

/* An integer or infinity clamped to [0, len]. */
static uint32_t clamp_to_length(double integer, uint32_t len) {
  if (!(integer > 0)) {
    return 0;
  }
  return integer >= len ? len : (uint32_t)integer;
}

/* A position argument (indexOf, substring, startsWith, …): clamped to [0, len]. */
static uint32_t clamped_position(double position, uint32_t len) {
  return clamp_to_length(to_integer_or_infinity(position), len);
}

/* A slice argument: a negative index counts back from the end. */
static uint32_t relative_position(double position, uint32_t len) {
  double integer = to_integer_or_infinity(position);
  return clamp_to_length(integer < 0 ? integer + len : integer, len);
}

/* ---------------------------------------------------------------- finding UTF-16 indices */

/* Where a UTF-16 index (at most the length) falls in a string's bytes. */
typedef struct utf16_position {
  uint32_t offset; /* byte offset of the character holding the unit, or byte_len at the end */
  bool low_half;   /* the unit is the second half of the surrogate pair encoded at offset */
} utf16_position;

/* Scans forward from a character boundary at byte offset `offset`, which is UTF-16 index `units`. */
static utf16_position locate_from(const pts_string *string, uint32_t offset, uint32_t units, uint32_t index) {
  while (units < index) {
    unsigned char lead = (unsigned char)string->bytes[offset];
    unsigned length = lead < 0x80 ? 1 : lead < 0xE0 ? 2 : lead < 0xF0 ? 3 : 4;
    if (length == 4 && units + 1 == index) {
      return (utf16_position){offset, true};
    }
    units += length == 4 ? 2 : 1;
    offset += length;
  }
  return (utf16_position){offset, false};
}

static utf16_position locate(const pts_string *string, uint32_t index) {
  if (string->flags & PTS_STRING_ASCII) {
    return (utf16_position){index, false};
  }
  return locate_from(string, 0, 0, index);
}

/* The byte offset of the first whole character at or after a position. */
static uint32_t next_boundary(utf16_position position) { return position.offset + (position.low_half ? 4 : 0); }

/* The number of UTF-16 units in bytes [from, to), which hold whole characters. */
static uint32_t count_units(const char *from, const char *to) {
  uint32_t units = 0;
  for (const unsigned char *b = (const unsigned char *)from; b < (const unsigned char *)to; b++) {
    if (!pts_utf8_is_continuation(*b)) {
      units += *b >= 0xF0 ? 2 : 1;
    }
  }
  return units;
}

/* Reads a string's UTF-16 units in order from some index. */
typedef struct unit_cursor {
  const pts_string *string;
  uint32_t pos;
  unsigned pending;
} unit_cursor;

static unit_cursor cursor_at(const pts_string *string, uint32_t index) {
  utf16_position position = locate(string, index);
  unit_cursor cursor = {string, position.offset, 0};
  if (position.low_half) {
    unsigned length;
    cursor.pending = pts_low_surrogate(pts_utf8_decode(string->bytes + position.offset, &length));
    cursor.pos += length;
  }
  return cursor;
}

static unsigned cursor_next(unit_cursor *cursor) {
  return pts_string_next_unit(cursor->string, &cursor->pos, &cursor->pending);
}

/* The code unit at index (below the length). */
static unsigned unit_at(const pts_string *string, uint32_t index) {
  if (string->flags & PTS_STRING_ASCII) {
    return (unsigned char)string->bytes[index];
  }
  unit_cursor cursor = cursor_at(string, index);
  return cursor_next(&cursor);
}

/* ---------------------------------------------------------------- building results */

/* A one-unit string. A surrogate unit is a lone surrogate. */
static pts_string *string_of_unit(unsigned unit) {
  if (unit < 0x80) {
    return (pts_string *)&ascii_chars[unit];
  }
  pts_string *string = pts_string_alloc(pts_utf8_length(unit));
  pts_utf8_encode(unit, string->bytes);
  string->flags = 0;
  string->utf16_len = 1;
  return string;
}

/* Bytes [start, end) of string, which hold whole characters, as a string. */
static pts_string *bytes_between(pts_string *string, uint32_t start, uint32_t end) {
  if (start == 0 && end == string->byte_len) {
    return string;
  }
  return pts_string_from_utf8(string->bytes + start, end - start);
}

/* Units [start, end) of string (start <= end <= length). A surrogate pair cut in half leaves a lone surrogate. */
static pts_string *units_between(pts_string *string, uint32_t start, uint32_t end) {
  if (start == end) {
    return empty_string;
  }
  if (string->flags & PTS_STRING_ASCII) {
    return bytes_between(string, start, end);
  }
  utf16_position from = locate(string, start);
  utf16_position to = locate_from(string, next_boundary(from), start + from.low_half, end);
  uint32_t copy_start = next_boundary(from);
  size_t byte_len = (from.low_half ? PTS_SURROGATE_BYTES : 0) + (to.offset - copy_start) + (to.low_half ? PTS_SURROGATE_BYTES : 0);
  pts_string *result = pts_string_alloc(byte_len);
  char *out = result->bytes;
  unsigned length;
  if (from.low_half) {
    out += pts_utf8_encode(pts_low_surrogate(pts_utf8_decode(string->bytes + from.offset, &length)), out);
  }
  memcpy(out, string->bytes + copy_start, to.offset - copy_start);
  out += to.offset - copy_start;
  if (to.low_half) {
    pts_utf8_encode(pts_high_surrogate(pts_utf8_decode(string->bytes + to.offset, &length)), out);
  }
  pts_string_measure(result);
  return result;
}

/* count copies of string, joined. */
static pts_string *repeated(pts_string *string, uint32_t count) {
  if (count == 0) {
    return empty_string;
  }
  if (pts_string_ends_with_high_surrogate(string) && pts_string_starts_with_low_surrogate(string)) {
    /* Every seam joins a surrogate pair, which concatenation does; double the string to stay O(n log n). */
    pts_string *result = empty_string, *power = string;
    for (;;) {
      if (count & 1) {
        result = pts_string_concat(result, power);
      }
      count >>= 1;
      if (count == 0) {
        return result;
      }
      power = pts_string_concat(power, power);
    }
  }
  uint64_t byte_len = (uint64_t)string->byte_len * count;
  if (byte_len > PTS_STRING_MAX_BYTES) {
    pts_throw_invalid_string_length();
  }
  pts_string *result = pts_string_alloc((size_t)byte_len);
  for (uint32_t i = 0; i < count; i++) {
    memcpy(result->bytes + (size_t)i * string->byte_len, string->bytes, string->byte_len);
  }
  result->flags = string->flags & PTS_STRING_ASCII;
  result->utf16_len = string->utf16_len * count;
  return result;
}

/* ---------------------------------------------------------------- indexing */

/* s[index] names a character only for an integer index in range (-0 included). */
static bool is_valid_index(double index, uint32_t len) { return index >= 0 && index < len && index == __builtin_trunc(index); }

pts_value pts_string_element(pts_string *string, double index) {
  if (!is_valid_index(index, string->utf16_len)) {
    return PTS_VALUE_UNDEFINED;
  }
  return pts_value_from_string(string_of_unit(unit_at(string, (uint32_t)index)));
}

pts_string *pts_string_element_checked(pts_string *string, double index) {
  if (!is_valid_index(index, string->utf16_len)) {
    char buf[PTS_NUMBER_BUF_SIZE];
    pts_number_format(index, buf);
    pts_panic("RangeError", "String index %s is out of range (length %" PRIu32 ")", buf, string->utf16_len);
  }
  return string_of_unit(unit_at(string, (uint32_t)index));
}

bool pts_string_element_equals(pts_string *string, double index, pts_string *other) {
  return other->utf16_len == 1 && is_valid_index(index, string->utf16_len) &&
         unit_at(string, (uint32_t)index) == unit_at(other, 0);
}

double pts_string_char_code_at(pts_string *string, double index) {
  double position = to_integer_or_infinity(index);
  if (position < 0 || position >= string->utf16_len) {
    return __builtin_nan("");
  }
  return unit_at(string, (uint32_t)position);
}

/* ---------------------------------------------------------------- substrings */

pts_string *pts_string_slice(pts_string *string, double start, double end) {
  uint32_t from = relative_position(start, string->utf16_len), to = relative_position(end, string->utf16_len);
  return from < to ? units_between(string, from, to) : empty_string;
}

pts_string *pts_string_substring(pts_string *string, double start, double end) {
  uint32_t a = clamped_position(start, string->utf16_len), b = clamped_position(end, string->utf16_len);
  return a <= b ? units_between(string, a, b) : units_between(string, b, a);
}

/* ---------------------------------------------------------------- searching */

/* Whether search's units match string's from the cursor on; the string must have enough units left. */
static bool units_match(unit_cursor cursor, const pts_string *search) {
  unit_cursor needle = {search, 0, 0};
  for (uint32_t i = 0; i < search->utf16_len; i++) {
    if (cursor_next(&cursor) != cursor_next(&needle)) {
      return false;
    }
  }
  return true;
}

/* Whether search occurs in string at unit index start. */
static bool matches_at(const pts_string *string, const pts_string *search, uint32_t start) {
  if (search->utf16_len > string->utf16_len - start) {
    return false;
  }
  if (string->flags & search->flags & PTS_STRING_ASCII) {
    return memcmp(string->bytes + start, search->bytes, search->byte_len) == 0;
  }
  return units_match(cursor_at(string, start), search);
}

/*
 * A search string that starts with a lone low surrogate or ends with a lone high one
 * can match half of a surrogate pair, which comparing bytes can't see.
 */
static bool can_match_half_a_pair(const pts_string *search) {
  return pts_string_starts_with_low_surrogate(search) || pts_string_ends_with_high_surrogate(search);
}

/* The first index at or after `from` (at most the length) where search occurs in string, or -1. */
static int64_t find(const pts_string *string, const pts_string *search, uint32_t from) {
  if (search->utf16_len > string->utf16_len - from) {
    return -1;
  }
  if (search->utf16_len == 0) {
    return from;
  }
  if (string->flags & search->flags & PTS_STRING_ASCII) {
    const char *found = memmem(string->bytes + from, string->byte_len - from, search->bytes, search->byte_len);
    return found == NULL ? -1 : found - string->bytes;
  }
  if (can_match_half_a_pair(search)) {
    unit_cursor cursor = cursor_at(string, from);
    for (uint32_t index = from; index + search->utf16_len <= string->utf16_len; index++) {
      if (units_match(cursor, search)) {
        return index;
      }
      cursor_next(&cursor);
    }
    return -1;
  }
  /* Matches start on whole characters, so a search from the middle of a pair starts after it. */
  utf16_position position = locate(string, from);
  uint32_t offset = next_boundary(position);
  const char *found = memmem(string->bytes + offset, string->byte_len - offset, search->bytes, search->byte_len);
  if (found == NULL) {
    return -1;
  }
  return from + position.low_half + count_units(string->bytes + offset, found);
}

double pts_string_index_of(pts_string *string, pts_string *search, double position) {
  return (double)find(string, search, clamped_position(position, string->utf16_len));
}

bool pts_string_includes(pts_string *string, pts_string *search, double position) {
  return find(string, search, clamped_position(position, string->utf16_len)) >= 0;
}

bool pts_string_starts_with(pts_string *string, pts_string *search, double position) {
  return matches_at(string, search, clamped_position(position, string->utf16_len));
}

bool pts_string_ends_with(pts_string *string, pts_string *search, double end_position) {
  uint32_t end = clamped_position(end_position, string->utf16_len);
  return search->utf16_len <= end && matches_at(string, search, end - search->utf16_len);
}

/* ---------------------------------------------------------------- trimming */

/* JS's WhiteSpace and LineTerminator: what trim removes. */
static bool is_js_whitespace(unsigned code_point) {
  switch (code_point) {
  case 0x09: case 0x0A: case 0x0B: case 0x0C: case 0x0D: case 0x20: case 0xA0: case 0x1680:
  case 0x2028: case 0x2029: case 0x202F: case 0x205F: case 0x3000: case 0xFEFF:
    return true;
  }
  return code_point >= 0x2000 && code_point <= 0x200A;
}

/* The byte offset just past a string's leading whitespace. */
static uint32_t leading_whitespace_end(const pts_string *string) {
  uint32_t offset = 0;
  while (offset < string->byte_len) {
    unsigned length;
    if (!is_js_whitespace(pts_utf8_decode(string->bytes + offset, &length))) {
      break;
    }
    offset += length;
  }
  return offset;
}

/* The byte offset where a string's trailing whitespace starts, but not before floor. */
static uint32_t trailing_whitespace_start(const pts_string *string, uint32_t floor) {
  uint32_t end = string->byte_len;
  while (end > floor) {
    uint32_t lead = end - 1;
    while (lead > floor && pts_utf8_is_continuation((unsigned char)string->bytes[lead])) {
      lead--;
    }
    unsigned length;
    if (!is_js_whitespace(pts_utf8_decode(string->bytes + lead, &length))) {
      break;
    }
    end = lead;
  }
  return end;
}

pts_string *pts_string_trim(pts_string *string) {
  uint32_t start = leading_whitespace_end(string);
  return bytes_between(string, start, trailing_whitespace_start(string, start));
}

pts_string *pts_string_trim_start(pts_string *string) {
  return bytes_between(string, leading_whitespace_end(string), string->byte_len);
}

pts_string *pts_string_trim_end(pts_string *string) { return bytes_between(string, 0, trailing_whitespace_start(string, 0)); }

/* ---------------------------------------------------------------- padding and repeating */

/* What padStart and padEnd add: fill repeated and cut to reach max_length units, or NULL if nothing. */
static pts_string *padding(const pts_string *string, double max_length, pts_string *fill) {
  double target = to_integer_or_infinity(max_length);
  if (target <= string->utf16_len || fill->utf16_len == 0) {
    return NULL;
  }
  if (target > PTS_STRING_MAX_BYTES) {
    pts_throw_invalid_string_length(); /* every unit takes at least a byte */
  }
  uint32_t units = (uint32_t)target - string->utf16_len;
  return pts_string_concat(repeated(fill, units / fill->utf16_len), units_between(fill, 0, units % fill->utf16_len));
}

pts_string *pts_string_pad_start(pts_string *string, double max_length, pts_string *fill) {
  pts_string *filler = padding(string, max_length, fill);
  return filler == NULL ? string : pts_string_concat(filler, string);
}

pts_string *pts_string_pad_end(pts_string *string, double max_length, pts_string *fill) {
  pts_string *filler = padding(string, max_length, fill);
  return filler == NULL ? string : pts_string_concat(string, filler);
}

pts_string *pts_string_repeat(pts_string *string, double count) {
  double n = to_integer_or_infinity(count);
  if (n < 0 || n == __builtin_inf()) {
    char buf[PTS_NUMBER_BUF_SIZE];
    pts_number_format(count, buf);
    pts_panic("RangeError", "Invalid count value: %s", buf);
  }
  if (n == 0 || string->byte_len == 0) {
    return empty_string;
  }
  if (n > PTS_STRING_MAX_BYTES) {
    pts_throw_invalid_string_length();
  }
  return repeated(string, (uint32_t)n);
}

pts_array *pts_string_split(pts_string *string, pts_string *separator, double limit) {
  pts_array *pieces = pts_array_of(PTS_FIELD_STRING, NULL, 0);
  /* JS's ToUint32 of the limit; a left-out one is Infinity, which means 2^32 - 1. */
  double max = limit != limit ? 0 : limit >= 4294967295.0 ? 4294967295.0 : limit <= 0 ? 0 : __builtin_trunc(limit);
  if (max == 0) {
    return pieces;
  }
  if (separator->utf16_len == 0) {
    for (uint32_t i = 0; i < string->utf16_len && i < max; i++) {
      pts_string *unit = string_of_unit(unit_at(string, i));
      pts_array_push(pieces, &unit, 1);
    }
    return pieces;
  }
  if (string->utf16_len == 0) {
    pts_array_push(pieces, &string, 1);
    return pieces;
  }
  uint32_t start = 0;
  for (int64_t match = find(string, separator, 0); match >= 0; match = find(string, separator, start)) {
    pts_string *piece = units_between(string, start, (uint32_t)match);
    if (pts_array_push(pieces, &piece, 1) == max) {
      return pieces;
    }
    start = (uint32_t)match + separator->utf16_len;
  }
  pts_string *rest = units_between(string, start, string->utf16_len);
  pts_array_push(pieces, &rest, 1);
  return pieces;
}
