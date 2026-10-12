/* RegExp objects and match arrays (D-133): exec's lastIndex rules, match arrays as Node
 * prints them, and RegExp.escape. Expected results are Node's. */
#include "pts.h"

#include "check.h"

#include <stdio.h>
#include <stdlib.h>

char *pts_inspect(pts_value value, double depth, bool show_hidden, size_t *len); /* inspect.c */

static pts_string *text(const char *cstr) { return pts_string_from_utf8(cstr, strlen(cstr)); }

static pts_regexp *regexp(const char *pattern, const char *flags) {
  return pts_regexp_new(pts_value_from_string(text(pattern)), pts_value_from_string(text(flags)));
}

/* What console.log prints for value, without the newline. */
static const char *inspected(pts_value value) {
  size_t len;
  return pts_inspect(value, 2, false, &len);
}

static const char *c_string(const pts_string *string) {
  char *copy = malloc(string->byte_len + 1);
  memcpy(copy, string->bytes, string->byte_len);
  copy[string->byte_len] = '\0';
  return copy;
}

static void exec_without_g_or_y_ignores_last_index(void) {
  pts_regexp *r = regexp("a", "");
  r->lastIndex = 5;
  CHECK_STR(inspected(pts_regexp_exec(r, text("xa"))), "[ 'a', index: 1, input: 'xa', groups: undefined ]");
  CHECK(r->lastIndex == 5);
}

static void global_exec_moves_last_index_and_resets_it_after_the_last_match(void) {
  pts_regexp *r = regexp("a", "g");
  CHECK(pts_regexp_test(r, text("aa")) && r->lastIndex == 1);
  CHECK(pts_regexp_test(r, text("aa")) && r->lastIndex == 2);
  CHECK(!pts_regexp_test(r, text("aa")) && r->lastIndex == 0);
  r->lastIndex = 7;
  CHECK(pts_regexp_exec(r, text("aa")) == PTS_VALUE_NULL && r->lastIndex == 0);
}

static void sticky_exec_matches_only_at_last_index(void) {
  pts_regexp *r = regexp("b", "y");
  CHECK(!pts_regexp_test(r, text("ab")) && r->lastIndex == 0);
  r->lastIndex = 1;
  CHECK(pts_regexp_test(r, text("ab")) && r->lastIndex == 2);
}

static void groups_that_took_no_part_are_undefined(void) {
  CHECK_STR(inspected(pts_regexp_exec(regexp("a(b)?", ""), text("xac"))), "[ 'a', undefined, index: 1, input: 'xac', groups: undefined ]");
  CHECK_STR(inspected(pts_regexp_exec(regexp("(?<a>x)|(?<b>y)", ""), text("y"))),
            "[\n  'y',\n  undefined,\n  'y',\n  index: 0,\n  input: 'y',\n  groups: [Object: null prototype] { a: undefined, b: 'y' }\n]");
}

static void copies_of_a_match_array_are_arrays_without_its_properties(void) {
  pts_array *match = (pts_array *)pts_value_as_object(pts_regexp_exec(regexp("(a)(b)", ""), text("ab")));
  pts_array *copy = pts_array_slice(match, 1, 3);
  CHECK(!pts_is_match_array((const pts_object *)copy));
  CHECK_STR(inspected(pts_value_from_object(copy)), "[ 'a', 'b' ]");
  pts_array *holes = pts_array_slice((pts_array *)pts_value_as_object(pts_regexp_exec(regexp("a(b)?", ""), text("a"))), 0, 2);
  CHECK_STR(inspected(pts_value_from_object(holes)), "[ 'a', undefined ]");
}

static void regexps_print_and_convert_as_their_literals(void) {
  pts_regexp *r = regexp("a/b\n", "gi");
  CHECK_STR(inspected(pts_value_from_object(r)), "/a\\/b\\n/gi");
  CHECK_STR(c_string(pts_value_to_string(pts_value_from_object(regexp("", "")))), "/(?:)/");
  CHECK_STR(c_string(pts_regexp_flags(regexp("x", "yusmigd"))), "dgimsuy");
}

static void escape_escapes_as_es2025_says(void) {
  CHECK_STR(c_string(pts_regexp_escape(text("a.b*c"))), "\\x61\\.b\\*c");
  CHECK_STR(c_string(pts_regexp_escape(text("1, -x\t"))), "\\x31\\x2c\\x20\\x2dx\\t");
  CHECK_STR(c_string(pts_regexp_escape(text("\xe3\x80\x80\xc3\xa9"))), "\\u3000\xc3\xa9");
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  exec_without_g_or_y_ignores_last_index();
  global_exec_moves_last_index_and_resets_it_after_the_last_match();
  sticky_exec_matches_only_at_last_index();
  groups_that_took_no_part_are_undefined();
  copies_of_a_match_array_are_arrays_without_its_properties();
  regexps_print_and_convert_as_their_literals();
  escape_escapes_as_es2025_says();
  return CHECK_DONE();
}
