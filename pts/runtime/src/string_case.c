/*
 * toUpperCase and toLowerCase: Unicode's full, locale-independent case mappings,
 * from tables generated out of Node's ICU (case_tables.h, D-016), plus the one
 * context-dependent rule of the root locale, Final_Sigma.
 */
#include "string_internal.h"

#include <stdlib.h>

enum { MAX_MAPPING_LENGTH = 3 };

typedef struct case_mapping {
  uint32_t from;
  uint32_t to[MAX_MAPPING_LENGTH]; /* unused entries are 0 */
} case_mapping;

typedef struct code_point_range {
  uint32_t first, last;
} code_point_range;

#include "case_tables.h"

#define COUNT(array) (sizeof(array) / sizeof((array)[0]))

enum { CAPITAL_SIGMA = 0x03A3, SMALL_SIGMA = 0x03C3, SMALL_FINAL_SIGMA = 0x03C2 };

typedef enum case_direction { TO_UPPER, TO_LOWER } case_direction;

static int compare_mapping(const void *key, const void *entry) {
  uint32_t code_point = *(const uint32_t *)key, from = ((const case_mapping *)entry)->from;
  return code_point < from ? -1 : code_point > from;
}

static bool in_ranges(const code_point_range *ranges, size_t count, uint32_t code_point) {
  size_t low = 0, high = count;
  while (low < high) {
    size_t mid = low + (high - low) / 2;
    if (code_point < ranges[mid].first) {
      high = mid;
    } else if (code_point > ranges[mid].last) {
      low = mid + 1;
    } else {
      return true;
    }
  }
  return false;
}

static bool is_cased(unsigned code_point) { return in_ranges(cased_ranges, COUNT(cased_ranges), code_point); }

static bool is_case_ignorable(unsigned code_point) {
  return in_ranges(case_ignorable_ranges, COUNT(case_ignorable_ranges), code_point);
}

/* Whether a cased letter comes before byte offset `offset`, skipping case-ignorable characters (as ICU does). */
static bool cased_letter_before(const pts_string *string, uint32_t offset) {
  while (offset > 0) {
    uint32_t lead = offset - 1;
    while (lead > 0 && pts_utf8_is_continuation((unsigned char)string->bytes[lead])) {
      lead--;
    }
    unsigned length, code_point = pts_utf8_decode(string->bytes + lead, &length);
    if (!is_case_ignorable(code_point)) {
      return is_cased(code_point);
    }
    offset = lead;
  }
  return false;
}

/* Whether a cased letter comes at or after byte offset `offset`, skipping case-ignorable characters. */
static bool cased_letter_after(const pts_string *string, uint32_t offset) {
  while (offset < string->byte_len) {
    unsigned length, code_point = pts_utf8_decode(string->bytes + offset, &length);
    if (!is_case_ignorable(code_point)) {
      return is_cased(code_point);
    }
    offset += length;
  }
  return false;
}

/* Final_Sigma (Unicode §3.13): a capital sigma at the end of a word lowercases to ς. */
static bool is_final_sigma(const pts_string *string, uint32_t offset, unsigned length) {
  return cased_letter_before(string, offset) && !cased_letter_after(string, offset + length);
}

/* Maps the character at byte offset `offset` into mapped and returns how many code points it became. */
static size_t map_character(const pts_string *string, uint32_t offset, case_direction direction, uint32_t *mapped,
                            unsigned *length) {
  unsigned code_point = pts_utf8_decode(string->bytes + offset, length);
  if (code_point < 0x80) {
    bool changes = direction == TO_UPPER ? code_point - 'a' < 26 : code_point - 'A' < 26;
    mapped[0] = changes ? code_point ^ 0x20 : code_point;
    return 1;
  }
  if (direction == TO_LOWER && code_point == CAPITAL_SIGMA) {
    mapped[0] = is_final_sigma(string, offset, *length) ? SMALL_FINAL_SIGMA : SMALL_SIGMA;
    return 1;
  }
  const case_mapping *mapping = direction == TO_UPPER
                                    ? bsearch(&code_point, upper_mappings, COUNT(upper_mappings), sizeof(case_mapping), compare_mapping)
                                    : bsearch(&code_point, lower_mappings, COUNT(lower_mappings), sizeof(case_mapping), compare_mapping);
  if (mapping == NULL) {
    mapped[0] = code_point;
    return 1;
  }
  size_t count = 0;
  while (count < MAX_MAPPING_LENGTH && mapping->to[count] != 0) {
    mapped[count] = mapping->to[count];
    count++;
  }
  return count;
}

/* Writes the mapped string to out, or with out NULL only measures it; returns its byte length. */
static size_t map_string(const pts_string *string, case_direction direction, char *out) {
  size_t written = 0;
  for (uint32_t offset = 0; offset < string->byte_len;) {
    uint32_t mapped[MAX_MAPPING_LENGTH];
    unsigned length;
    size_t count = map_character(string, offset, direction, mapped, &length);
    for (size_t i = 0; i < count; i++) {
      written += out == NULL ? pts_utf8_length(mapped[i]) : pts_utf8_encode(mapped[i], out + written);
    }
    offset += length;
  }
  return written;
}

static pts_string *convert_ascii(pts_string *string, case_direction direction) {
  unsigned char first = direction == TO_UPPER ? 'a' : 'A';
  uint32_t i = 0;
  while (i < string->byte_len && (unsigned char)(string->bytes[i] - first) >= 26) {
    i++;
  }
  if (i == string->byte_len) {
    return string; /* nothing to change */
  }
  pts_string *result = pts_string_alloc(string->byte_len);
  memcpy(result->bytes, string->bytes, string->byte_len);
  for (; i < string->byte_len; i++) {
    if ((unsigned char)(result->bytes[i] - first) < 26) {
      result->bytes[i] ^= 0x20;
    }
  }
  result->flags = PTS_STRING_ASCII;
  result->utf16_len = string->utf16_len;
  return result;
}

static pts_string *convert_case(pts_string *string, case_direction direction) {
  if (string->flags & PTS_STRING_ASCII) {
    return convert_ascii(string, direction);
  }
  pts_string *result = pts_string_alloc(map_string(string, direction, NULL));
  map_string(string, direction, result->bytes);
  pts_string_measure(result);
  return result;
}

pts_string *pts_string_to_upper_case(pts_string *string) { return convert_case(string, TO_UPPER); }

pts_string *pts_string_to_lower_case(pts_string *string) { return convert_case(string, TO_LOWER); }
