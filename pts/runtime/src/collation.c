/*
 * String.prototype.localeCompare (D-082): the order of Node's default collator (ICU's
 * root collation at tertiary strength) from per-code-point weights generated out of
 * Node's ICU. Strings compare by their primary weights (base letters), then their
 * secondary weights (accents), then their tertiary weights (case and variants),
 * skipping a code point's weight where it is 0 (ignorable at that level). Hangul
 * syllables decompose into their jamo, and code points outside the tables' blocks
 * (Han and the rare scripts) take implicit weights after them in code point order, Han
 * first, as ICU does. Characters that ICU expands into several (ß as "ss", æ as "ae",
 * ligatures) compare as single letters of their own.
 */
#include "string_internal.h"

typedef struct collation_block {
  uint32_t first, last;
  const uint32_t *weights;
} collation_block;

#include "collation_tables.h"

enum { LEVELS = 3 };

/* A code point's weights at each level: primary, secondary, tertiary. */
typedef struct collation_key {
  uint32_t weight[LEVELS];
} collation_key;

static collation_key key_of_packed(uint32_t packed) { return (collation_key){{packed >> 16, (packed >> 8) & 0xFF, packed & 0xFF}}; }

static bool is_core_han(unsigned cp) {
  if (cp >= 0x4E00 && cp <= 0x9FFF) {
    return true;
  }
  switch (cp) { /* the unified ideographs among the compatibility ideographs */
  case 0xFA0E: case 0xFA0F: case 0xFA11: case 0xFA13: case 0xFA14: case 0xFA1F:
  case 0xFA21: case 0xFA23: case 0xFA24: case 0xFA27: case 0xFA28: case 0xFA29:
    return true;
  default:
    return false;
  }
}

static bool is_other_han(unsigned cp) { return (cp >= 0x3400 && cp <= 0x4DBF) || (cp >= 0x20000 && cp <= 0x323AF); }

/* ICU's implicit weights: after every weight in the tables, core Han first, other Han next, everything else last. */
static collation_key implicit_key(unsigned cp) {
  uint32_t group = is_core_han(cp) ? 0 : is_other_han(cp) ? 1 : 2;
  return (collation_key){{0x10000 + group * 0x200000 + cp, 1, 1}};
}

static collation_key key_of(unsigned cp) {
  for (size_t i = 0; i < sizeof collation_blocks / sizeof collation_blocks[0]; i++) {
    if (cp >= collation_blocks[i].first && cp <= collation_blocks[i].last) {
      return key_of_packed(collation_blocks[i].weights[cp - collation_blocks[i].first]);
    }
  }
  return implicit_key(cp);
}

/* ---------------------------------------------------------------- reading keys */

enum {
  HANGUL_FIRST = 0xAC00, HANGUL_LAST = 0xD7A3,
  JAMO_L = 0x1100, JAMO_V = 0x1161, JAMO_T = 0x11A7,
  JAMO_V_COUNT = 21, JAMO_T_COUNT = 28,
};

/* Reads a string's collation keys in order: one per code point, two or three per Hangul syllable. */
typedef struct key_cursor {
  const pts_string *string;
  uint32_t pos;
  unsigned pending[2]; /* the jamo still to come of the syllable just read, last first */
  unsigned pending_count;
} key_cursor;

static bool cursor_next_key(key_cursor *cursor, collation_key *key) {
  if (cursor->pending_count > 0) {
    *key = key_of(cursor->pending[--cursor->pending_count]);
    return true;
  }
  if (cursor->pos >= cursor->string->byte_len) {
    return false;
  }
  unsigned length;
  unsigned cp = pts_utf8_decode(cursor->string->bytes + cursor->pos, &length);
  cursor->pos += length;
  if (cp >= HANGUL_FIRST && cp <= HANGUL_LAST) {
    unsigned index = cp - HANGUL_FIRST, t = index % JAMO_T_COUNT;
    if (t != 0) {
      cursor->pending[cursor->pending_count++] = JAMO_T + t;
    }
    cursor->pending[cursor->pending_count++] = JAMO_V + index / JAMO_T_COUNT % JAMO_V_COUNT;
    cp = JAMO_L + index / (JAMO_T_COUNT * JAMO_V_COUNT);
  }
  *key = key_of(cp);
  return true;
}

/* The next weight at level that isn't 0, or 0 at the end of the string. */
static uint32_t next_weight(key_cursor *cursor, int level) {
  collation_key key;
  while (cursor_next_key(cursor, &key)) {
    if (key.weight[level] != 0) {
      return key.weight[level];
    }
  }
  return 0;
}

static int compare_at_level(const pts_string *a, const pts_string *b, int level) {
  key_cursor x = {a, 0, {0}, 0}, y = {b, 0, {0}, 0};
  for (;;) {
    uint32_t wa = next_weight(&x, level), wb = next_weight(&y, level);
    if (wa != wb) {
      return wa < wb ? -1 : 1;
    }
    if (wa == 0) {
      return 0;
    }
  }
}

double pts_string_locale_compare(pts_string *string, pts_string *that) {
  if (string->byte_len == that->byte_len && memcmp(string->bytes, that->bytes, string->byte_len) == 0) {
    return 0;
  }
  for (int level = 0; level < LEVELS; level++) {
    int order = compare_at_level(string, that, level);
    if (order != 0) {
      return order;
    }
  }
  return 0;
}
