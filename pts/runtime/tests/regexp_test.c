/* The RegExp core (D-132): JS patterns translated to PCRE2, matched in UTF-16 indices,
 * and V8's SyntaxErrors. Expected results are Node's (the `d` flag's indices). */
#include "pts.h"

#include "check.h"

#include <stdio.h>

static pts_string *text(const char *bytes, size_t len) { return pts_string_from_utf8(bytes, len); }

#define STRING(literal) text((literal), sizeof(literal) - 1)

/* found describes a match as Node's indices print it ("1:4 0:1", -1:-1 for a group that took no part), or "none". */
static const char *found(const char *pattern, const char *flags, pts_string *subject, uint32_t from, bool anchored) {
  static char described[256];
  pts_regex *regex = pts_regex_compile(text(pattern, strlen(pattern)), text(flags, strlen(flags)));
  int64_t captures[2 * 16];
  pts_regex_cursor cursor = {0, 0};
  if (!pts_regex_exec(regex, subject, from, anchored, &cursor, captures)) {
    return "none";
  }
  size_t used = 0;
  for (uint32_t g = 0; g <= pts_regex_group_count(regex); g++) {
    used += (size_t)snprintf(described + used, sizeof described - used, "%s%lld:%lld", g == 0 ? "" : " ", (long long)captures[2 * g],
                             (long long)captures[2 * g + 1]);
  }
  return described;
}

/* compile_error is the message of the SyntaxError compiling pattern throws, or "no error". */
static const char *compile_error(const char *pattern, const char *flags) {
  static char message[256];
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_regex_compile(text(pattern, strlen(pattern)), text(flags, strlen(flags)));
    pts_try_leave(&handler);
    return "no error";
  }
  pts_value caught = pts_caught(&handler);
  CHECK(pts_value_instance_of(caught, &pts_type_builtin_SyntaxError));
  pts_string *string = pts_value_to_string(caught);
  snprintf(message, sizeof message, "%.*s", (int)string->byte_len, string->bytes);
  return message;
}

static void plain_patterns_match_as_in_js(void) {
  CHECK_STR(found("a+b", "", STRING("xaab"), 0, false), "1:4");
  CHECK_STR(found("a{2,}", "", STRING("aaa"), 0, false), "0:3");
  CHECK_STR(found("x*", "", STRING("abc"), 3, false), "3:3");
  CHECK_STR(found("(?:)", "", STRING(""), 0, false), "0:0");
  CHECK_STR(found("(a)|(b)", "", STRING("b"), 0, false), "0:1 -1:-1 0:1");
  CHECK_STR(found("b", "", STRING("ab"), 1, true), "1:2");
  CHECK_STR(found("a", "", STRING("ab"), 1, true), "none");
}

static void line_terminators_are_js_ones(void) {
  CHECK_STR(found("^b", "m", STRING("a\rb"), 0, false), "2:3");
  CHECK_STR(found("a$", "", STRING("a\n"), 0, false), "none");
  CHECK_STR(found("a$", "m", STRING("a\n"), 0, false), "0:1");
  CHECK_STR(found(".", "", STRING("\u2028x"), 0, false), "1:2");
  CHECK_STR(found(".", "s", STRING("\n"), 0, false), "0:1");
  CHECK_STR(found("^", "gm", STRING("a\nb"), 1, false), "2:2");
}

static void escapes_mean_what_they_mean_in_js(void) {
  CHECK_STR(found("\\v", "", STRING("a\vb"), 0, false), "1:2");
  CHECK_STR(found("\\v", "", STRING("\n"), 0, false), "none");
  CHECK_STR(found("\\a\\e\\z", "", STRING("aez"), 0, false), "0:3");
  CHECK_STR(found("\\u0041\\x42", "", STRING("zAB"), 0, false), "1:3");
  CHECK_STR(found("\\ud83d\\ude00", "", STRING("a\U0001F600"), 0, false), "1:3");
  CHECK_STR(found("\\u{1F600}", "u", STRING("a\U0001F600"), 0, false), "1:3");
  CHECK_STR(found("\\0", "", STRING("a\0"), 0, false), "1:2");
  CHECK_STR(found("\\cJ", "", STRING("\n"), 0, false), "0:1");
  CHECK_STR(found("\\/", "", STRING("a/b"), 0, false), "1:2");
  CHECK_STR(found("a{", "", STRING("a{"), 0, false), "0:2");
}

static void classes_are_js_classes(void) {
  CHECK_STR(found("[[:a]+", "", STRING("x[:a"), 0, false), "1:4");
  CHECK_STR(found("[\\w-.]+", "", STRING("a-b.c"), 0, false), "0:5");
  CHECK_STR(found("[\\d-z]+", "", STRING("1-z"), 0, false), "0:3");
  CHECK_STR(found("[]", "", STRING("abc"), 0, false), "none");
  CHECK_STR(found("[^]", "", STRING("\n"), 0, false), "0:1");
  CHECK_STR(found("\\s+", "", STRING("a\u00a0\u3000b"), 0, false), "1:3");
  CHECK_STR(found("\\d", "", STRING("\u0663"), 0, false), "none");
  CHECK_STR(found("\\w", "", STRING("\u00e9"), 0, false), "none");
  CHECK_STR(found("\\b\u00e9", "", STRING("a\u00e9"), 0, false), "1:2");
  CHECK_STR(found("\\p{Lu}+", "u", STRING("abCDe"), 0, false), "2:4");
  CHECK_STR(found("\\p{Script=Greek}", "u", STRING("a\u03b2"), 0, false), "1:2");
  CHECK_STR(found("\\p{Letter}+", "u", STRING("1ab2"), 0, false), "1:3");
}

static void case_folding_follows_the_unicode_flag(void) {
  CHECK_STR(found("K", "i", STRING("k"), 0, false), "0:1");
  CHECK_STR(found("k", "i", STRING("\u212a"), 0, false), "none");
  CHECK_STR(found("k", "iu", STRING("\u212a"), 0, false), "0:1");
  CHECK_STR(found("(?i:a)b", "", STRING("Ab"), 0, false), "0:2");
  CHECK_STR(found("(?i:a)b", "", STRING("AB"), 0, false), "none");
}

static void groups_and_references(void) {
  CHECK_STR(found("(a)\\1", "", STRING("aa"), 0, false), "0:2 0:1");
  CHECK_STR(found("\\1(a)", "", STRING("a"), 0, false), "0:1 0:1");
  CHECK_STR(found("(?<y>a)\\k<y>", "", STRING("baa"), 0, false), "1:3 1:2");
  CHECK_STR(found("(?<=\\$)\\d+", "", STRING("$42"), 0, false), "1:3");
  pts_regex *date = pts_regex_compile(STRING("(?<year>\\d{4})-(?<month>\\d\\d)(x)?"), STRING(""));
  CHECK(pts_regex_group_count(date) == 3);
  CHECK(pts_string_equals(pts_regex_group_name(date, 1), STRING("year")));
  CHECK(pts_string_equals(pts_regex_group_name(date, 2), STRING("month")));
  CHECK(pts_regex_group_name(date, 3) == NULL && pts_regex_group_name(date, 4) == NULL);
  CHECK_STR(found("(?<year>\\d{4})-(?<month>\\d\\d)", "", STRING("on 2024-05"), 0, false), "3:10 3:7 8:10");
}

static void indices_count_utf16_units(void) {
  CHECK_STR(found("\u00e9+", "", STRING("a\u00e9\u00e9\u20acb"), 0, false), "1:3");
  CHECK_STR(found("b", "", STRING("\U0001F600b"), 0, false), "2:3");
  CHECK_STR(found("(\u20ac)(b)", "", STRING("\U0001F600\u20acb"), 0, false), "2:4 2:3 3:4");
  CHECK_STR(found("b", "", STRING("\U0001F600b\u20acb"), 3, false), "4:5");
  /* A lone surrogate (WTF-8, D-014) is one unit that nothing in a pattern matches (D-132). */
  CHECK_STR(found("b", "", STRING("a\xED\xA0\x80" "b"), 0, false), "2:3");
}

/* A loop over matches carries the cursor forward, so each search starts where the last match ended. */
static void a_cursor_carries_positions_between_matches(void) {
  pts_regex *word = pts_regex_compile(STRING("\\w+"), STRING("g"));
  pts_string *subject = STRING("\u00e9t\u00e9 one \U0001F600 two");
  pts_regex_cursor cursor = {0, 0};
  int64_t captures[2];
  uint32_t from = 0;
  char seen[64] = "";
  while (pts_regex_exec(word, subject, from, false, &cursor, captures)) {
    snprintf(seen + strlen(seen), sizeof seen - strlen(seen), "%lld:%lld ", (long long)captures[0], (long long)captures[1]);
    CHECK(cursor.unit == (uint32_t)captures[1]);
    from = (uint32_t)captures[1];
  }
  CHECK_STR(seen, "1:2 4:7 11:14 ");
}

static void invalid_patterns_throw_v8s_syntax_errors(void) {
  CHECK_STR(compile_error("(", ""), "SyntaxError: Invalid regular expression: /(/: Unterminated group");
  CHECK_STR(compile_error(")", ""), "SyntaxError: Invalid regular expression: /)/: Unmatched ')'");
  CHECK_STR(compile_error("*", ""), "SyntaxError: Invalid regular expression: /*/: Nothing to repeat");
  CHECK_STR(compile_error("^*", ""), "SyntaxError: Invalid regular expression: /^*/: Nothing to repeat");
  CHECK_STR(compile_error("[a", ""), "SyntaxError: Invalid regular expression: /[a/: Unterminated character class");
  CHECK_STR(compile_error("\\", ""), "SyntaxError: Invalid regular expression: /\\/: \\ at end of pattern");
  CHECK_STR(compile_error("[z-a]", ""), "SyntaxError: Invalid regular expression: /[z-a]/: Range out of order in character class");
  CHECK_STR(compile_error("a{2,1}", ""), "SyntaxError: Invalid regular expression: /a{2,1}/: numbers out of order in {} quantifier");
  CHECK_STR(compile_error("a", "gg"), "SyntaxError: Invalid flags supplied to RegExp constructor 'gg'");
  CHECK_STR(compile_error("(?<a>x)(?<a>y)", ""), "SyntaxError: Invalid regular expression: /(?<a>x)(?<a>y)/: Duplicate capture group name");
  CHECK_STR(compile_error("\\k<b>(?<a>x)", ""), "SyntaxError: Invalid regular expression: /\\k<b>(?<a>x)/: Invalid named capture referenced");
  CHECK_STR(compile_error("\\a", "u"), "SyntaxError: Invalid regular expression: /\\a/u: Invalid escape");
  CHECK_STR(compile_error("(?x)", ""), "SyntaxError: Invalid regular expression: /(?x)/: Invalid group");
  CHECK_STR(compile_error("a/(", "g"), "SyntaxError: Invalid regular expression: /a/(/g: Unterminated group");
  CHECK_STR(compile_error("{", "u"), "SyntaxError: Invalid regular expression: /{/u: Lone quantifier brackets");
}

static void sources_escape_slashes_and_line_terminators(void) {
  CHECK(pts_string_equals(pts_regex_escape_source(STRING("a/b")), STRING("a\\/b")));
  CHECK(pts_string_equals(pts_regex_escape_source(STRING("")), STRING("(?:)")));
  CHECK(pts_string_equals(pts_regex_escape_source(STRING("[/]")), STRING("[/]")));
  CHECK(pts_string_equals(pts_regex_escape_source(STRING("\n")), STRING("\\n")));
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  plain_patterns_match_as_in_js();
  line_terminators_are_js_ones();
  escapes_mean_what_they_mean_in_js();
  classes_are_js_classes();
  case_folding_follows_the_unicode_flag();
  groups_and_references();
  indices_count_utf16_units();
  a_cursor_carries_positions_between_matches();
  invalid_patterns_throw_v8s_syntax_errors();
  sources_escape_slashes_and_line_terminators();
  return CHECK_DONE();
}
