/* The String methods that take a RegExp (D-134): GetSubstitution's references, split's
 * captures and limits, empty matches, and what the loops leave in lastIndex. Expected
 * results are Node's, as JSON. */
#include "pts.h"

#include "check.h"

#include <stdio.h>
#include <stdlib.h>

static pts_string *text(const char *cstr) { return pts_string_from_utf8(cstr, strlen(cstr)); }

static pts_value regexp(const char *pattern, const char *flags) {
  return pts_value_from_object(pts_regexp_new(pts_value_from_string(text(pattern)), pts_value_from_string(text(flags))));
}

/* JSON.stringify of value, as a C string. */
static const char *json(pts_value value) {
  pts_string *string = pts_value_as_string(pts_json_stringify(value, PTS_VALUE_UNDEFINED, PTS_VALUE_UNDEFINED));
  char *copy = malloc(string->byte_len + 1);
  memcpy(copy, string->bytes, string->byte_len);
  copy[string->byte_len] = '\0';
  return copy;
}

static const char *replaced(const char *subject, const char *pattern, const char *flags, const char *template) {
  return json(pts_value_from_string(pts_string_replace_value(text(subject), regexp(pattern, flags), text(template), false)));
}

static const char *split(const char *subject, const char *pattern, const char *flags, pts_value limit) {
  return json(pts_value_from_object(pts_string_split_value(text(subject), regexp(pattern, flags), limit)));
}

static void references_follow_get_substitution(void) {
  CHECK_STR(replaced("x-y", "(\\w)-(\\w)", "", "$2$1|$3|$10|$01|$00|$<n>|$"), "\"yx|$3|x0|x|$00|$<n>|$\"");
  CHECK_STR(replaced("ab", "(?<n>a)", "", "[$<n>|$<m>|$<n]"), "\"[a||$<n]b\"");
  CHECK_STR(replaced("a1b", "\\d", "", "$`-$'-$$-$&"), "\"aa-b-$-1b\"");
}

static void split_keeps_captures_and_stops_at_the_limit(void) {
  CHECK_STR(split("a,b,,c", ",", "", pts_value_from_number(3)), "[\"a\",\"b\",\"\"]");
  CHECK_STR(split("a1b2c", "(\\d)", "", PTS_VALUE_UNDEFINED), "[\"a\",\"1\",\"b\",\"2\",\"c\"]");
  CHECK_STR(split("ab", "(?:)", "", PTS_VALUE_UNDEFINED), "[\"a\",\"b\"]");
  CHECK_STR(split("\xf0\x9f\x98\x80" "a", "(?:)", "u", PTS_VALUE_UNDEFINED), "[\"\xf0\x9f\x98\x80\",\"a\"]");
  CHECK_STR(split("", "a*", "", PTS_VALUE_UNDEFINED), "[]");
  CHECK_STR(split("", "b", "", PTS_VALUE_UNDEFINED), "[\"\"]");
}

static void empty_matches_advance_by_a_character(void) {
  CHECK_STR(json(pts_value_from_string(pts_string_replace_value(text("\xf0\x9f\x98\x80\xf0\x9f\x98\x80"), regexp("(?:)", "gu"), text("-"), false))),
            "\"-\xf0\x9f\x98\x80-\xf0\x9f\x98\x80-\"");
}

static void loops_leave_last_index_as_js_does(void) {
  pts_value global = regexp("a", "g");
  ((pts_regexp *)pts_value_as_object(global))->lastIndex = 1;
  CHECK_STR(json(pts_string_match(text("aXa"), global)), "[\"a\",\"a\"]");
  CHECK(((pts_regexp *)pts_value_as_object(global))->lastIndex == 0);
  ((pts_regexp *)pts_value_as_object(global))->lastIndex = 2;
  CHECK(pts_string_search(text("bab"), global) == 1);
  CHECK(((pts_regexp *)pts_value_as_object(global))->lastIndex == 2);
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  references_follow_get_substitution();
  split_keeps_captures_and_stops_at_the_limit();
  empty_matches_advance_by_a_character();
  loops_leave_last_index_as_js_does();
  return CHECK_DONE();
}
