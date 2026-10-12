/*
 * String.prototype.normalize (D-135): Unicode's normalization forms NFC, NFD, NFKC and
 * NFKD (UAX #15) over tables generated from Node's ICU (normalization_tables.h): each
 * character's full decomposition, canonical ordering by combining class, and canonical
 * composition with blocking, Hangul by arithmetic. Strings are WTF-8 (D-014): a lone
 * surrogate is a starter that nothing decomposes or composes.
 */
#include "pts.h"
#include "string_internal.h"

#include <stdlib.h>
#include <string.h>

typedef struct decomposition {
  uint32_t code_point;
  uint32_t start; /* in decomposition_pool */
  uint32_t length;
} decomposition;

typedef struct class_range {
  uint32_t first, last;
  uint8_t rank;
} class_range;

typedef struct composition {
  uint32_t first, second, composite;
} composition;

#include "normalization_tables.h"

enum {
  HANGUL_FIRST = 0xAC00, /* the syllables, L * 588 + V * 28 + T from here */
  HANGUL_COUNT = 11172,
  HANGUL_L = 0x1100,
  HANGUL_V = 0x1161,
  HANGUL_T = 0x11A7, /* one before the first trailing consonant */
  HANGUL_L_COUNT = 19,
  HANGUL_V_COUNT = 21,
  HANGUL_T_COUNT = 28,
  HANGUL_N_COUNT = HANGUL_V_COUNT * HANGUL_T_COUNT,
};

/* ---------------------------------------------------------------- the tables */

static const decomposition *find_decomposition(const decomposition *table, size_t count, uint32_t cp) {
  size_t low = 0, high = count;
  while (low < high) {
    size_t mid = (low + high) / 2;
    if (table[mid].code_point == cp) {
      return &table[mid];
    }
    if (table[mid].code_point < cp) {
      low = mid + 1;
    } else {
      high = mid;
    }
  }
  return NULL;
}

/* The rank of cp's canonical combining class: 0 for a starter; only the order of ranks matters. */
static uint8_t class_rank(uint32_t cp) {
  size_t low = 0, high = sizeof combining_classes / sizeof combining_classes[0];
  while (low < high) {
    size_t mid = (low + high) / 2;
    if (cp < combining_classes[mid].first) {
      high = mid;
    } else if (cp > combining_classes[mid].last) {
      low = mid + 1;
    } else {
      return combining_classes[mid].rank;
    }
  }
  return 0;
}

/* The primary composite of first followed by second, or 0 if they don't compose. */
static uint32_t composite_of(uint32_t first, uint32_t second) {
  if (first >= HANGUL_L && first < HANGUL_L + HANGUL_L_COUNT && second >= HANGUL_V && second < HANGUL_V + HANGUL_V_COUNT) {
    return HANGUL_FIRST + ((first - HANGUL_L) * HANGUL_V_COUNT + (second - HANGUL_V)) * HANGUL_T_COUNT;
  }
  if (first >= HANGUL_FIRST && first < HANGUL_FIRST + HANGUL_COUNT && (first - HANGUL_FIRST) % HANGUL_T_COUNT == 0 && second > HANGUL_T &&
      second < HANGUL_T + HANGUL_T_COUNT) {
    return first + (second - HANGUL_T);
  }
  size_t low = 0, high = sizeof compositions / sizeof compositions[0];
  while (low < high) {
    size_t mid = (low + high) / 2;
    const composition *c = &compositions[mid];
    if (c->first == first && c->second == second) {
      return c->composite;
    }
    if (c->first < first || (c->first == first && c->second < second)) {
      low = mid + 1;
    } else {
      high = mid;
    }
  }
  return 0;
}

/* ---------------------------------------------------------------- code point buffers */

typedef struct code_points {
  uint32_t *at;
  size_t len, cap;
} code_points;

static void append(code_points *cps, const uint32_t *more, size_t count) {
  if (cps->len + count > cps->cap) {
    cps->cap = (cps->len + count) * 2;
    cps->at = realloc(cps->at, cps->cap * sizeof *cps->at);
    if (cps->at == NULL) {
      pts_panic("RangeError", "Out of memory normalizing a string");
    }
  }
  memcpy(cps->at + cps->len, more, count * sizeof *more);
  cps->len += count;
}

/* Appends cp's full decomposition (compatibility too if compatible), or cp itself. */
static void decompose(code_points *out, uint32_t cp, bool compatible) {
  if (cp >= HANGUL_FIRST && cp < HANGUL_FIRST + HANGUL_COUNT) {
    uint32_t index = cp - HANGUL_FIRST;
    uint32_t jamo[] = {HANGUL_L + index / HANGUL_N_COUNT, HANGUL_V + (index % HANGUL_N_COUNT) / HANGUL_T_COUNT, HANGUL_T + index % HANGUL_T_COUNT};
    append(out, jamo, jamo[2] == HANGUL_T ? 2 : 3);
    return;
  }
  const decomposition *d = NULL;
  if (compatible) {
    d = find_decomposition(compatibility_decompositions, sizeof compatibility_decompositions / sizeof compatibility_decompositions[0], cp);
  }
  if (d == NULL) {
    d = find_decomposition(canonical_decompositions, sizeof canonical_decompositions / sizeof canonical_decompositions[0], cp);
  }
  if (d == NULL) {
    append(out, &cp, 1);
  } else {
    append(out, decomposition_pool + d->start, d->length);
  }
}

/* Canonical ordering: each run of non-starters sorted stably by class (an insertion sort; runs are short). */
static void reorder(code_points *cps) {
  for (size_t i = 1; i < cps->len; i++) {
    uint32_t cp = cps->at[i];
    uint8_t rank = class_rank(cp);
    if (rank == 0) {
      continue;
    }
    size_t j = i;
    while (j > 0 && class_rank(cps->at[j - 1]) > rank) {
      cps->at[j] = cps->at[j - 1];
      j--;
    }
    cps->at[j] = cp;
  }
}

/* Canonical composition: each character joins the last starter if nothing between blocks it. */
static void compose(code_points *cps) {
  if (cps->len == 0) {
    return;
  }
  size_t starter = 0, kept = 1;
  int last_rank = class_rank(cps->at[0]) == 0 ? 0 : 256; /* of the last character kept after the starter; 256: no starter yet */
  for (size_t i = 1; i < cps->len; i++) {
    uint32_t cp = cps->at[i];
    int rank = class_rank(cp);
    uint32_t composite = last_rank == 256 ? 0 : composite_of(cps->at[starter], cp);
    if (composite != 0 && (last_rank < rank || last_rank == 0)) {
      cps->at[starter] = composite;
      continue;
    }
    if (rank == 0) {
      starter = kept;
    }
    last_rank = rank;
    cps->at[kept++] = cp;
  }
  cps->len = kept;
}

/* ---------------------------------------------------------------- normalize */

/* The form named by form (undefined: NFC): whether it composes and whether it decomposes compatibility characters. */
static void parse_form(pts_value form, bool *composed, bool *compatible) {
  *composed = true, *compatible = false;
  if (form == PTS_VALUE_UNDEFINED) {
    return;
  }
  const pts_string *name = pts_value_to_string(form);
  static const char *const names[] = {"NFC", "NFD", "NFKC", "NFKD"};
  for (int i = 0; i < 4; i++) {
    if (name->byte_len == strlen(names[i]) && memcmp(name->bytes, names[i], name->byte_len) == 0) {
      *composed = i % 2 == 0, *compatible = i >= 2;
      return;
    }
  }
  pts_throw_new(&pts_type_builtin_RangeError, "The normalization form should be one of NFC, NFD, NFKC, NFKD.");
}

pts_string *pts_string_normalize(pts_string *string, pts_value form) {
  bool composed, compatible;
  parse_form(form, &composed, &compatible);
  if ((string->flags & PTS_STRING_ASCII) != 0) {
    return string; /* every form of ASCII is itself */
  }
  code_points cps = {0};
  for (uint32_t k = 0; k < string->byte_len;) {
    unsigned len;
    decompose(&cps, pts_utf8_decode(string->bytes + k, &len), compatible);
    k += len;
  }
  reorder(&cps);
  if (composed) {
    compose(&cps);
  }
  char *bytes = malloc(cps.len * 4 + 1);
  if (bytes == NULL) {
    pts_panic("RangeError", "Out of memory normalizing a string");
  }
  size_t n = 0;
  for (size_t i = 0; i < cps.len; i++) {
    n += pts_utf8_encode(cps.at[i], bytes + n);
  }
  free(cps.at);
  pts_string *result = n == string->byte_len && memcmp(bytes, string->bytes, n) == 0 ? string : pts_string_from_utf8(bytes, n);
  free(bytes);
  return result;
}
