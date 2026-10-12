/*
 * RegExp objects and the arrays exec makes (D-133), over the core in regexp.c (D-132).
 * A RegExp is its compiled code, the pattern as written and lastIndex; the flags are the
 * code's. exec and test match as RegExpBuiltinExec does, reading lastIndex only for the
 * g and y flags and setting it after a match (or to 0 after none) only for them.
 */
#include "pts.h"
#include "regexp_internal.h"
#include "string_internal.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

const pts_type pts_type_builtin_RegExp = {
    .kind = PTS_KIND_REGEXP,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_regexp, regex) | PTS_GC_WORD(pts_regexp, pattern)),
    .name = "RegExp",
    .size = sizeof(pts_regexp),
};

const pts_type pts_type_match_array = {
    .kind = PTS_KIND_ARRAY,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_match_array, array.buffer) | PTS_GC_WORD(pts_match_array, input) |
                                     PTS_GC_WORD(pts_match_array, groups)),
    .name = "Array",
    .size = sizeof(pts_match_array),
    .element = PTS_FIELD_STRING,
};

/* A copy of a match array: a string array whose holes are undefined. */
const pts_type pts_type_match_copy = {
    .kind = PTS_KIND_ARRAY,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_array, buffer)),
    .name = "Array",
    .size = sizeof(pts_array),
    .element = PTS_FIELD_STRING,
};

/* A match's groups object: JS makes it with a null prototype, which Node prints. */
static const pts_type groups_type = {
    .kind = PTS_KIND_RECORD,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
    .name = "[Object: null prototype]",
    .size = sizeof(pts_map),
    .key = PTS_FIELD_STRING,
    .element = PTS_FIELD_STRING,
};

/* ---------------------------------------------------------------- making */

static pts_regexp *regexp_new(pts_regex *regex, pts_string *pattern) {
  pts_regexp *regexp = pts_object_new(&pts_type_builtin_RegExp);
  regexp->regex = regex;
  regexp->pattern = pattern;
  return regexp;
}

bool pts_is_regexp(pts_value value) { return pts_value_is_object(value) && pts_unfrozen(pts_value_as_object(value)->type) == &pts_type_builtin_RegExp; }

void pts_regexp_set_last_index(pts_regexp *regexp, double last_index) {
  if (regexp->pts_header->frozen) {
    pts_throw_read_only((const pts_object *)regexp, "lastIndex"); /* D-157 */
  }
  regexp->lastIndex = last_index;
}

pts_regexp *pts_regexp_literal(pts_regex **cache, pts_string *pattern, pts_string *flags) {
  pts_regex *regex = __atomic_load_n(cache, __ATOMIC_ACQUIRE);
  if (regex == NULL) { /* threads racing here each compile it; the code is immutable, so any of them will do */
    regex = pts_regex_compile(pattern, flags);
    __atomic_store_n(cache, regex, __ATOMIC_RELEASE);
  }
  return regexp_new(regex, pattern);
}

pts_regexp *pts_regexp_new(pts_value pattern, pts_value flags) {
  PTS_STATIC_STRING(empty, PTS_STRING_ASCII, 0, 0, "");
  if (pts_is_regexp(pattern)) {
    const pts_regexp *original = (const pts_regexp *)pts_value_as_object(pattern);
    if (flags == PTS_VALUE_UNDEFINED) {
      return regexp_new(original->regex, original->pattern);
    }
    return regexp_new(pts_regex_compile(original->pattern, pts_value_to_string(flags)), original->pattern);
  }
  pts_string *text = pattern == PTS_VALUE_UNDEFINED ? empty : pts_value_to_string(pattern);
  pts_string *letters = flags == PTS_VALUE_UNDEFINED ? empty : pts_value_to_string(flags);
  return regexp_new(pts_regex_compile(text, letters), text);
}

pts_regexp *pts_regexp_call(pts_value pattern, pts_value flags) {
  if (pts_is_regexp(pattern) && flags == PTS_VALUE_UNDEFINED) {
    return (pts_regexp *)pts_value_as_object(pattern);
  }
  return pts_regexp_new(pattern, flags);
}

/* ---------------------------------------------------------------- properties */

pts_string *pts_regexp_source(const pts_regexp *regexp) { return pts_regex_escape_source(regexp->pattern); }

pts_string *pts_regexp_flags(const pts_regexp *regexp) {
  static const struct {
    unsigned bit;
    char letter;
  } order[] = {
      {PTS_REGEX_HAS_INDICES, 'd'}, {PTS_REGEX_GLOBAL, 'g'},  {PTS_REGEX_IGNORE_CASE, 'i'}, {PTS_REGEX_MULTILINE, 'm'},
      {PTS_REGEX_DOT_ALL, 's'},     {PTS_REGEX_UNICODE, 'u'}, {PTS_REGEX_STICKY, 'y'},
  };
  unsigned bits = pts_regex_flags(regexp->regex);
  char letters[sizeof order / sizeof order[0]];
  size_t count = 0;
  for (size_t i = 0; i < sizeof order / sizeof order[0]; i++) {
    if ((bits & order[i].bit) != 0) {
      letters[count++] = order[i].letter;
    }
  }
  return pts_string_from_utf8(letters, count);
}

bool pts_regexp_has_flag(const pts_regexp *regexp, double flag) { return (pts_regex_flags(regexp->regex) & (unsigned)flag) != 0; }

pts_string *pts_regexp_to_string(const pts_regexp *regexp) {
  pts_string *source = pts_regexp_source(regexp), *flags = pts_regexp_flags(regexp);
  size_t len = 2 + (size_t)source->byte_len + flags->byte_len;
  pts_string *text = pts_string_alloc(len);
  text->bytes[0] = '/';
  memcpy(text->bytes + 1, source->bytes, source->byte_len);
  text->bytes[1 + source->byte_len] = '/';
  memcpy(text->bytes + 2 + source->byte_len, flags->bytes, flags->byte_len);
  pts_string_measure(text);
  return text;
}

/* ---------------------------------------------------------------- matching */

static const double MAX_LENGTH = 9007199254740991.0; /* 2^53 - 1, ToLength's limit */

static double to_length(double n) {
  if (!(n > 0)) {
    return 0; /* NaN too */
  }
  n = __builtin_floor(n);
  return n < MAX_LENGTH ? n : MAX_LENGTH;
}

int64_t *pts_regexp_captures_for(const pts_regex *regex, int64_t *local) {
  size_t count = 2 * ((size_t)pts_regex_group_count(regex) + 1);
  if (count <= PTS_LOCAL_CAPTURES) {
    return local;
  }
  int64_t *captures = GC_MALLOC_ATOMIC(count * sizeof *captures);
  if (captures == NULL) {
    pts_panic("RangeError", "Out of memory matching a regular expression");
  }
  return captures;
}

bool pts_regexp_match(pts_regexp *regexp, pts_string *subject, int64_t *captures, pts_regex_cursor *cursor) {
  unsigned flags = pts_regex_flags(regexp->regex);
  bool sticky = (flags & PTS_REGEX_STICKY) != 0;
  bool global_or_sticky = sticky || (flags & PTS_REGEX_GLOBAL) != 0;
  double from = global_or_sticky ? to_length(regexp->lastIndex) : 0;
  if (from < cursor->unit) {
    *cursor = (pts_regex_cursor){0, 0}; /* the program moved lastIndex back */
  }
  if (from > subject->utf16_len || !pts_regex_exec(regexp->regex, subject, (uint32_t)from, sticky, cursor, captures)) {
    if (global_or_sticky) {
      pts_regexp_set_last_index(regexp, 0);
    }
    return false;
  }
  if (global_or_sticky) {
    pts_regexp_set_last_index(regexp, (double)captures[1]);
  }
  return true;
}

pts_value pts_regexp_exec(pts_regexp *regexp, pts_string *subject) {
  int64_t local[PTS_LOCAL_CAPTURES];
  int64_t *captures = pts_regexp_captures_for(regexp->regex, local);
  pts_regex_cursor cursor = {0, 0};
  if (!pts_regexp_match(regexp, subject, captures, &cursor)) {
    return PTS_VALUE_NULL;
  }
  return pts_value_from_object(pts_match_array_new(regexp->regex, subject, captures));
}

bool pts_regexp_test(pts_regexp *regexp, pts_string *subject) {
  int64_t local[PTS_LOCAL_CAPTURES];
  pts_regex_cursor cursor = {0, 0};
  return pts_regexp_match(regexp, subject, pts_regexp_captures_for(regexp->regex, local), &cursor);
}

/* ---------------------------------------------------------------- match arrays */

/* The groups object of a match: each named group's text, or a hole where it took no part; undefined without names. */
static pts_value named_groups(const pts_regex *regex, pts_string *const *texts) {
  pts_map *groups = NULL;
  for (uint32_t g = 1; g <= pts_regex_group_count(regex); g++) {
    pts_string *name = pts_regex_group_name(regex, g);
    if (name == NULL) {
      continue;
    }
    if (groups == NULL) {
      groups = pts_map_new(&groups_type);
    }
    pts_map_set(groups, &name, &texts[g]);
  }
  return groups == NULL ? PTS_VALUE_UNDEFINED : pts_value_from_object(groups);
}

pts_match_array *pts_match_array_new(const pts_regex *regex, pts_string *subject, const int64_t *captures) {
  return pts_match_array_at(regex, subject, captures, (pts_regex_cursor){0, 0});
}

pts_match_array *pts_match_array_at(const pts_regex *regex, pts_string *subject, const int64_t *captures, pts_regex_cursor start) {
  uint32_t groups = pts_regex_group_count(regex);
  pts_match_array *match = (pts_match_array *)pts_array_of_length(&pts_type_match_array, (double)groups + 1);
  pts_string **texts = (pts_string **)match->array.buffer->elements; /* not yet shared, so plain stores */
  for (uint32_t g = 0; g <= groups; g++) {
    if (captures[2 * g] >= 0) {
      pts_regex_cursor at = start; /* groups start at or after the match, except a lookbehind's, which the slice finds from the start */
      texts[g] = pts_regex_slice(subject, &at, (uint32_t)captures[2 * g], (uint32_t)captures[2 * g + 1]);
    }
  }
  match->index = (double)captures[0];
  match->input = subject;
  match->groups = named_groups(regex, texts);
  return match;
}

pts_value pts_match_index(const pts_array *array) {
  return pts_is_match_array((const pts_object *)array) ? pts_value_from_number(((const pts_match_array *)array)->index) : PTS_VALUE_UNDEFINED;
}

pts_value pts_match_input(const pts_array *array) {
  return pts_is_match_array((const pts_object *)array) ? pts_value_from_string(((const pts_match_array *)array)->input) : PTS_VALUE_UNDEFINED;
}

pts_value pts_match_groups(const pts_array *array) {
  return pts_is_match_array((const pts_object *)array) ? ((const pts_match_array *)array)->groups : PTS_VALUE_UNDEFINED;
}

/* ---------------------------------------------------------------- RegExp.escape */

static bool is_white_space_or_line_terminator(unsigned c) {
  switch (c) {
  case 0x09: case 0x0A: case 0x0B: case 0x0C: case 0x0D: case 0x20: case 0xA0: case 0x1680:
  case 0x2028: case 0x2029: case 0x202F: case 0x205F: case 0x3000: case 0xFEFF:
    return true;
  default:
    return c >= 0x2000 && c <= 0x200A;
  }
}

static bool is_ascii_alphanumeric(unsigned c) { return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'); }

/* Whether c is one of the ASCII characters of the C string set (never NUL). */
static bool among(unsigned c, const char *set) { return c != 0 && c < 0x80 && strchr(set, (int)c) != NULL; }

/* EncodeForRegExpEscape, with the first character's escape when it is a digit or letter; writes to out and returns the byte count. */
static size_t encode_for_escape(unsigned c, const char *bytes, unsigned len, bool first, char *out) {
  static const char control_letters[] = "tnvfr"; /* U+0009 … U+000D */
  if (first && is_ascii_alphanumeric(c)) {
    return (size_t)sprintf(out, "\\x%02x", c);
  }
  if (among(c, "^$\\.*+?()[]{}|/")) {
    out[0] = '\\', out[1] = (char)c;
    return 2;
  }
  if (c >= 0x09 && c <= 0x0D) {
    out[0] = '\\', out[1] = control_letters[c - 0x09];
    return 2;
  }
  if (among(c, ",-=<>#&!%:;@~'`\"") || is_white_space_or_line_terminator(c) || (c >= 0xD800 && c <= 0xDFFF)) {
    return (size_t)sprintf(out, c <= 0xFF ? "\\x%02x" : "\\u%04x", c);
  }
  memcpy(out, bytes, len);
  return len;
}

pts_string *pts_regexp_escape(pts_string *text) {
  enum { MAX_GROWTH = 4 }; /* "\x2c" for a 1-byte character; "　" for a 3-byte one */
  char *out = malloc((size_t)text->byte_len * MAX_GROWTH + 1);
  if (out == NULL) {
    pts_panic("RangeError", "Out of memory escaping a string for a regular expression");
  }
  size_t n = 0;
  for (uint32_t k = 0; k < text->byte_len;) {
    unsigned len;
    unsigned c = pts_utf8_decode(text->bytes + k, &len);
    n += encode_for_escape(c, text->bytes + k, len, k == 0, out + n);
    k += len;
  }
  pts_string *escaped = pts_string_from_utf8(out, n);
  free(out);
  return escaped;
}
