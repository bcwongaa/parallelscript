/*
 * The String methods that take a RegExp (D-134): match, matchAll, search, split, replace
 * and replaceAll, as RegExp.prototype's @@match, @@matchAll, @@search, @@split and
 * @@replace do them, over regexp_object.c's matching. Loops over a string's matches carry
 * a cursor, so they convert positions in one pass, and move past an empty match by one
 * character (AdvanceStringIndex). A pattern that isn't a RegExp is a string, which split
 * and replace use as one and the others make a RegExp of, as JS does.
 *
 * The loops over every match of a global RegExp keep their position to themselves and
 * leave lastIndex at 0 when they end, as JS does: no program code runs between their
 * steps, so no program sees the difference, and threads may share the RegExp.
 */
#include "pts.h"
#include "regexp_internal.h"
#include "string_internal.h"

#include <string.h>

static const double MAX_UINT32 = 4294967295.0;

static pts_string *empty(void) {
  PTS_STATIC_STRING(text, PTS_STRING_ASCII, 0, 0, "");
  return text;
}

/* The RegExp a pattern is: itself, or a new one of its string with the given flags. */
static pts_regexp *regexp_of(pts_value pattern, const char *flags) {
  if (pts_is_regexp(pattern)) {
    return (pts_regexp *)pts_value_as_object(pattern);
  }
  return pts_regexp_new(pattern, pts_value_from_string(pts_string_from_cstr(flags)));
}

static bool has_flag(const pts_regexp *regexp, unsigned flag) { return (pts_regex_flags(regexp->regex) & flag) != 0; }

/* JS's SameValue of two numbers. */
static bool same_value(double a, double b) { return a != a ? b != b : a == b && __builtin_signbit(a) == __builtin_signbit(b); }

/*
 * The next match of a global loop at *from (with y) or after it, which then moves past the
 * match: to its end, or after an empty one by one unit, or with u by a whole surrogate
 * pair (AdvanceStringIndex). *cursor, at or before *from, moves to the match's end.
 */
static bool next_match(const pts_regexp *regexp, pts_string *subject, double *from, int64_t *captures, pts_regex_cursor *cursor) {
  if (*from > subject->utf16_len || !pts_regex_exec(regexp->regex, subject, (uint32_t)*from, has_flag(regexp, PTS_REGEX_STICKY), cursor, captures)) {
    return false;
  }
  *from = (double)captures[1];
  if (captures[0] == captures[1]) {
    bool pair = has_flag(regexp, PTS_REGEX_UNICODE) && cursor->byte < subject->byte_len && (unsigned char)subject->bytes[cursor->byte] >= 0xF0;
    *from += pair ? 2 : 1;
  }
  return true;
}

/*
 * The next match of replace's loop: every match from the start with g (lastIndex being 0
 * after the last), else the one exec finds (reading and setting lastIndex with y).
 */
static bool next_replaced(pts_regexp *regexp, pts_string *subject, bool global, double *from, int64_t *captures, pts_regex_cursor *cursor) {
  if (!global) {
    bool first = *from == 0;
    *from = __builtin_inf(); /* one match at most */
    return first && pts_regexp_match(regexp, subject, captures, cursor);
  }
  if (next_match(regexp, subject, from, captures, cursor)) {
    return true;
  }
  pts_regexp_set_last_index(regexp, 0);
  return false;
}

/* ---------------------------------------------------------------- match, matchAll, search */

pts_value pts_string_match(pts_string *subject, pts_value pattern) {
  pts_regexp *regexp = regexp_of(pattern, "");
  if (!has_flag(regexp, PTS_REGEX_GLOBAL)) {
    return pts_regexp_exec(regexp, subject);
  }
  int64_t local[PTS_LOCAL_CAPTURES];
  int64_t *captures = pts_regexp_captures_for(regexp->regex, local);
  pts_array *matches = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  pts_regex_cursor cursor = {0, 0};
  double from = 0;
  while (true) {
    pts_regex_cursor start = cursor;
    if (!next_match(regexp, subject, &from, captures, &cursor)) {
      break;
    }
    pts_string *text = pts_regex_slice(subject, &start, (uint32_t)captures[0], (uint32_t)captures[1]);
    pts_array_push(matches, &text, 1);
  }
  pts_regexp_set_last_index(regexp, 0);
  pts_publish();
  return pts_array_length_of(matches) == 0 ? PTS_VALUE_NULL : pts_value_from_object(matches);
}

pts_value pts_string_match_all(pts_string *subject, pts_value pattern) {
  pts_regexp *matcher;
  if (pts_is_regexp(pattern)) {
    pts_regexp *regexp = (pts_regexp *)pts_value_as_object(pattern);
    if (!has_flag(regexp, PTS_REGEX_GLOBAL)) {
      pts_throw_new(&pts_type_builtin_TypeError, "String.prototype.matchAll called with a non-global RegExp argument");
    }
    matcher = regexp; /* JS clones it, so its lastIndex, where the matches start, stays as it is */
  } else {
    matcher = regexp_of(pattern, "g");
  }
  int64_t local[PTS_LOCAL_CAPTURES];
  int64_t *captures = pts_regexp_captures_for(matcher->regex, local);
  pts_array *matches = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  pts_regex_cursor cursor = {0, 0};
  double from = matcher->lastIndex > 0 ? __builtin_floor(matcher->lastIndex) : 0; /* ToLength; NaN is 0 */
  while (true) {
    pts_regex_cursor start = cursor;
    if (!next_match(matcher, subject, &from, captures, &cursor)) {
      break;
    }
    pts_value match = pts_value_from_object(pts_match_array_at(matcher->regex, subject, captures, start));
    pts_array_push(matches, &match, 1);
  }
  PTS_STATIC_STRING(text, PTS_STRING_ASCII, 8, 8, "matchAll");
  return pts_iterator_get(pts_value_from_object(matches), text);
}

double pts_string_search(pts_string *subject, pts_value pattern) {
  pts_regexp *regexp = regexp_of(pattern, "");
  double previous = regexp->lastIndex;
  if (!same_value(previous, 0)) { /* as JS writes it: a frozen RegExp throws only for a write (D-157) */
    pts_regexp_set_last_index(regexp, 0);
  }
  int64_t local[PTS_LOCAL_CAPTURES];
  int64_t *captures = pts_regexp_captures_for(regexp->regex, local);
  pts_regex_cursor cursor = {0, 0};
  bool found = pts_regexp_match(regexp, subject, captures, &cursor);
  if (!same_value(regexp->lastIndex, previous)) {
    pts_regexp_set_last_index(regexp, previous);
  }
  return found ? (double)captures[0] : -1;
}

/* ---------------------------------------------------------------- split */

/* ToUint32 of a split's limit; undefined is no limit. */
static double split_limit(pts_value limit) {
  if (limit == PTS_VALUE_UNDEFINED) {
    return MAX_UINT32;
  }
  double n = pts_value_to_number(limit);
  if (n != n || n == __builtin_inf() || n == -__builtin_inf()) {
    return 0;
  }
  n = __builtin_fmod(__builtin_trunc(n), 4294967296.0);
  return n < 0 ? n + 4294967296.0 : n;
}

/* Pushes text, a string or NULL for undefined, and reports whether the result reached the limit. */
static bool push_piece(pts_array *pieces, pts_string *text, double limit) {
  pts_array_push(pieces, &text, 1);
  return pts_array_length(pieces) >= limit;
}

/*
 * @@split with a sticky splitter: each match that ends somewhere other than where the last
 * piece ended cuts a piece and adds its captures (holes for those that took no part). A
 * search from q finds the first position at or after q where the sticky splitter would match.
 */
static pts_array *split_regexp(pts_string *subject, pts_regexp *regexp, double limit) {
  pts_array *pieces = pts_array_of(&pts_type_match_copy, NULL, 0);
  if (limit == 0) {
    return pieces;
  }
  const pts_regex *regex = regexp->regex;
  int64_t local[PTS_LOCAL_CAPTURES];
  int64_t *captures = pts_regexp_captures_for(regex, local);
  uint32_t size = subject->utf16_len, groups = pts_regex_group_count(regex);
  pts_regex_cursor search = {0, 0};
  if (size == 0) {
    if (!pts_regex_exec(regex, subject, 0, true, &search, captures)) {
      push_piece(pieces, subject, limit);
    }
    return pieces;
  }
  uint32_t p = 0, q = 0;                  /* where the last piece ended; where to search from */
  pts_regex_cursor piece_start = {0, 0}; /* at p */
  while (q < size) {
    pts_regex_cursor at = search;
    if (!pts_regex_exec(regex, subject, q, false, &at, captures) || captures[0] >= size) {
      break;
    }
    uint32_t start = (uint32_t)captures[0], end = (uint32_t)captures[1];
    if (end == p) {
      q = start + 1; /* an empty match where the last piece ended */
      continue;
    }
    if (push_piece(pieces, pts_regex_slice(subject, &piece_start, p, start), limit)) {
      return pieces;
    }
    for (uint32_t g = 1; g <= groups; g++) {
      pts_regex_cursor group_start = search;
      pts_string *text = captures[2 * g] < 0 ? NULL : pts_regex_slice(subject, &group_start, (uint32_t)captures[2 * g], (uint32_t)captures[2 * g + 1]);
      if (push_piece(pieces, text, limit)) {
        return pieces;
      }
    }
    p = q = end;
    search = at;
    piece_start = at;
  }
  push_piece(pieces, pts_regex_slice(subject, &piece_start, p, size), limit);
  return pieces;
}

pts_array *pts_string_split_value(pts_string *subject, pts_value separator, pts_value limit) {
  if (pts_is_regexp(separator)) {
    pts_array *pieces = split_regexp(subject, (pts_regexp *)pts_value_as_object(separator), split_limit(limit));
    pts_publish();
    return pieces;
  }
  if (separator == PTS_VALUE_UNDEFINED) {
    return pts_array_of(&pts_array_types[PTS_FIELD_STRING], &subject, split_limit(limit) == 0 ? 0 : 1);
  }
  return pts_string_split(subject, pts_value_to_string(separator), split_limit(limit));
}

/* ---------------------------------------------------------------- replace */

/* What the matches of a replacement are found in, and what the substitutions read. */
typedef struct replacing {
  pts_string *subject;
  const pts_regex *regex;
  const int64_t *captures;
  pts_regex_cursor start; /* at or before the match */
} replacing;

static pts_string *group_text(const replacing *r, uint32_t g) {
  if (r->captures[2 * g] < 0) {
    return empty();
  }
  pts_regex_cursor at = r->start;
  return pts_regex_slice(r->subject, &at, (uint32_t)r->captures[2 * g], (uint32_t)r->captures[2 * g + 1]);
}

/* The group named name (len bytes), or 0 if there is none. */
static uint32_t group_named(const pts_regex *regex, const char *name, size_t len) {
  for (uint32_t g = 1; g <= pts_regex_group_count(regex); g++) {
    const pts_string *group = pts_regex_group_name(regex, g);
    if (group != NULL && group->byte_len == len && memcmp(group->bytes, name, len) == 0) {
      return g;
    }
  }
  return 0;
}

static bool has_named_groups(const pts_regex *regex) {
  for (uint32_t g = 1; g <= pts_regex_group_count(regex); g++) {
    if (pts_regex_group_name(regex, g) != NULL) {
      return true;
    }
  }
  return false;
}

/*
 * What the reference at template's byte i (a `$`) stands for, setting *used to its length;
 * NULL for a `$` that stays as it is. GetSubstitution: $$, $&, $`, $', $n and $nn (two
 * digits if that group exists), $<name>.
 */
static pts_string *reference(const replacing *r, const pts_string *template, uint32_t i, uint32_t *used) {
  const char *t = template->bytes + i;
  uint32_t left = template->byte_len - i;
  uint32_t groups = pts_regex_group_count(r->regex);
  *used = 2;
  switch (left < 2 ? '\0' : t[1]) {
  case '$': {
    PTS_STATIC_STRING(dollar, PTS_STRING_ASCII, 1, 1, "$");
    return dollar;
  }
  case '&':
    return group_text(r, 0);
  case '`': {
    pts_regex_cursor at = {0, 0};
    return pts_regex_slice(r->subject, &at, 0, (uint32_t)r->captures[0]);
  }
  case '\'': {
    pts_regex_cursor at = r->start;
    return pts_regex_slice(r->subject, &at, (uint32_t)r->captures[1], r->subject->utf16_len);
  }
  case '<': {
    const char *close = memchr(t + 2, '>', left - 2);
    if (!has_named_groups(r->regex) || close == NULL) {
      return NULL;
    }
    *used = (uint32_t)(close - t) + 1;
    uint32_t g = group_named(r->regex, t + 2, (size_t)(close - t - 2));
    return g == 0 ? empty() : group_text(r, g);
  }
  default:
    break;
  }
  if (left < 2 || t[1] < '0' || t[1] > '9') {
    return NULL;
  }
  uint32_t g = (uint32_t)(t[1] - '0');
  if (left >= 3 && t[2] >= '0' && t[2] <= '9' && g * 10 + (uint32_t)(t[2] - '0') >= 1 && g * 10 + (uint32_t)(t[2] - '0') <= groups) {
    g = g * 10 + (uint32_t)(t[2] - '0');
    *used = 3;
  }
  return g >= 1 && g <= groups ? group_text(r, g) : NULL;
}

/* GetSubstitution of template for the match r describes. */
static pts_string *substitution(const replacing *r, pts_string *template) {
  if (memchr(template->bytes, '$', template->byte_len) == NULL) {
    return template;
  }
  pts_string *result = empty();
  uint32_t copied = 0; /* bytes of template already in result */
  for (uint32_t i = 0; i < template->byte_len; i++) {
    uint32_t used;
    pts_string *part = template->bytes[i] == '$' ? reference(r, template, i, &used) : NULL;
    if (part == NULL) {
      continue;
    }
    result = pts_string_concat(result, pts_string_from_utf8(template->bytes + copied, i - copied));
    result = pts_string_concat(result, part);
    copied = i + used;
    i += used - 1;
  }
  return pts_string_concat(result, pts_string_from_utf8(template->bytes + copied, template->byte_len - copied));
}

/* TypeError for replaceAll of a RegExp without g, as V8 words it. */
static void require_global(const pts_regexp *regexp, bool all) {
  if (all && !has_flag(regexp, PTS_REGEX_GLOBAL)) {
    pts_throw_new(&pts_type_builtin_TypeError, "String.prototype.replaceAll called with a non-global RegExp argument");
  }
}

/* @@replace with a template: each match (every one with g, else the one exec finds) replaced by its substitution. */
static pts_string *replace_regexp(pts_string *subject, pts_regexp *regexp, pts_string *template, bool all) {
  require_global(regexp, all);
  bool global = has_flag(regexp, PTS_REGEX_GLOBAL);
  int64_t local[PTS_LOCAL_CAPTURES];
  int64_t *captures = pts_regexp_captures_for(regexp->regex, local);
  pts_regex_cursor cursor = {0, 0}, copied_to = {0, 0}; /* the end of the last match; where the result's copy of subject reached */
  pts_string *result = empty();
  uint32_t next = 0; /* the end of the last match */
  bool replaced = false;
  double from = 0;
  while (true) {
    pts_regex_cursor start = cursor;
    if (!next_replaced(regexp, subject, global, &from, captures, &cursor)) {
      break;
    }
    replacing r = {subject, regexp->regex, captures, start};
    uint32_t position = (uint32_t)captures[0];
    if (position >= next) {
      result = pts_string_concat(result, pts_regex_slice(subject, &copied_to, next, position));
      result = pts_string_concat(result, substitution(&r, template));
      next = (uint32_t)captures[1];
      replaced = true;
    }
  }
  if (!replaced) {
    return subject;
  }
  return pts_string_concat(result, pts_regex_slice(subject, &copied_to, next, subject->utf16_len));
}

pts_string *pts_string_replace_value(pts_string *subject, pts_value pattern, pts_string *replacement, bool all) {
  if (pts_is_regexp(pattern)) {
    return replace_regexp(subject, (pts_regexp *)pts_value_as_object(pattern), replacement, all);
  }
  pts_string *search = pts_value_to_string(pattern);
  return all ? pts_string_replace_all(subject, search, replacement) : pts_string_replace(subject, search, replacement);
}

/* ---------------------------------------------------------------- replace with a callback */

pts_array *pts_regexp_replace_matches(pts_string *subject, pts_regexp *regexp, bool all) {
  require_global(regexp, all);
  bool global = has_flag(regexp, PTS_REGEX_GLOBAL);
  int64_t local[PTS_LOCAL_CAPTURES];
  int64_t *captures = pts_regexp_captures_for(regexp->regex, local);
  pts_array *matches = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  pts_regex_cursor cursor = {0, 0};
  double from = 0;
  while (true) {
    pts_regex_cursor start = cursor;
    if (!next_replaced(regexp, subject, global, &from, captures, &cursor)) {
      break;
    }
    pts_value match = pts_value_from_object(pts_match_array_at(regexp->regex, subject, captures, start));
    pts_array_push(matches, &match, 1);
  }
  return matches;
}

pts_array *pts_regexp_replacer_arguments(pts_value match) {
  const pts_match_array *found = (const pts_match_array *)pts_value_as_object(match);
  size_t count = pts_array_length_of(&found->array);
  pts_array *args = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  for (size_t i = 0; i < count; i++) {
    pts_value element = pts_array_element(&found->array, (double)i); /* undefined for a group that took no part */
    pts_array_push(args, &element, 1);
  }
  pts_value tail[] = {pts_value_from_number(found->index), pts_value_from_string(found->input), found->groups};
  pts_array_push(args, tail, found->groups == PTS_VALUE_UNDEFINED ? 2 : 3);
  return args;
}

pts_string *pts_regexp_replace_with(pts_string *subject, pts_array *matches, pts_array *replacements) {
  size_t count = pts_array_length_of(matches);
  if (count == 0) {
    return subject;
  }
  pts_string *result = empty();
  pts_regex_cursor copied_to = {0, 0};
  uint32_t next = 0;
  for (size_t i = 0; i < count; i++) {
    const pts_match_array *found = (const pts_match_array *)pts_value_as_object(pts_array_element(matches, (double)i));
    uint32_t position = (uint32_t)found->index;
    const pts_string *matched = pts_value_as_string(pts_array_element(&found->array, 0));
    if (position >= next) {
      result = pts_string_concat(result, pts_regex_slice(subject, &copied_to, next, position));
      result = pts_string_concat(result, pts_value_as_string(pts_array_element(replacements, (double)i)));
      next = position + matched->utf16_len;
    }
  }
  return pts_string_concat(result, pts_regex_slice(subject, &copied_to, next, subject->utf16_len));
}
