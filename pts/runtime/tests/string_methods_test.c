#include "pts.h"

#include "check.h"

#include <math.h>

/* "\uD83D" and "\uDE00", the halves of U+1F600, as lone surrogates (WTF-8). */
#define HIGH "\xED\xA0\xBD"
#define LOW "\xED\xB8\x80"
#define GRIN "\xF0\x9F\x98\x80"

static const double NO_START = NAN, TO_END = INFINITY;

static pts_string *s(const char *cstr) { return pts_string_from_cstr(cstr); }

static const char *element(const char *string, double index) {
  pts_value value = pts_string_element(s(string), index);
  return value == PTS_VALUE_UNDEFINED ? "undefined" : ((pts_string *)pts_value_as_object(value))->bytes;
}

static void element_reads_one_code_unit_or_undefined(void) {
  CHECK_STR(element("abc", 1), "b");
  CHECK_STR(element("abc", -0.0), "a");
  CHECK_STR(element("abc", 3), "undefined");
  CHECK_STR(element("abc", -1), "undefined");
  CHECK_STR(element("abc", 1.5), "undefined");
  CHECK_STR(element("abc", NAN), "undefined");
  CHECK_STR(element("é" GRIN "a", 0), "é");
  CHECK_STR(element("é" GRIN "a", 1), HIGH);
  CHECK_STR(element("é" GRIN "a", 2), LOW);
  CHECK_STR(element("é" GRIN "a", 3), "a");
  CHECK(pts_string_element_checked(s("xyz"), 2)->flags & PTS_STRING_ASCII);
  CHECK(pts_string_element_checked(s("x" GRIN), 1)->utf16_len == 1);
}

static void ascii_elements_do_not_allocate(void) {
  pts_string *text = s("aa");
  CHECK(pts_string_element_checked(text, 0) == pts_string_element_checked(text, 1));
}

static void element_equals_compares_one_unit(void) {
  CHECK(pts_string_element_equals(s("a" GRIN), 1, s(HIGH)));
  CHECK(!pts_string_element_equals(s("a" GRIN), 1, s(GRIN)));
  CHECK(pts_string_element_equals(s("abc"), 2, s("c")));
  CHECK(!pts_string_element_equals(s("abc"), 3, s("c")));
  CHECK(!pts_string_element_equals(s("abc"), 0, s("ab")));
}

static void char_code_at_truncates_and_gives_nan_out_of_range(void) {
  pts_string *text = s("é" GRIN);
  CHECK(pts_string_char_code_at(text, 0) == 0xE9);
  CHECK(pts_string_char_code_at(text, 1.7) == 0xD83D);
  CHECK(pts_string_char_code_at(text, 2) == 0xDE00);
  CHECK(isnan(pts_string_char_code_at(text, 3)));
  CHECK(isnan(pts_string_char_code_at(text, -1)));
  CHECK(pts_string_char_code_at(text, -0.5) == 0xE9);
  CHECK(pts_string_char_code_at(s("A"), NAN) == 65);
}

static void slice_counts_from_the_end_and_clamps(void) {
  pts_string *text = s("hello");
  CHECK_STR(pts_string_slice(text, 1, 3)->bytes, "el");
  CHECK_STR(pts_string_slice(text, -3, TO_END)->bytes, "llo");
  CHECK_STR(pts_string_slice(text, NO_START, -1)->bytes, "hell");
  CHECK_STR(pts_string_slice(text, 3, 1)->bytes, "");
  CHECK_STR(pts_string_slice(text, -INFINITY, 99)->bytes, "hello");
  CHECK(pts_string_slice(text, NO_START, TO_END) == text);
}

static void slice_cuts_surrogate_pairs_into_lone_surrogates(void) {
  pts_string *text = s("a" GRIN "b");
  pts_string *tail = pts_string_slice(text, 2, TO_END);
  CHECK_STR(tail->bytes, LOW "b");
  CHECK(tail->utf16_len == 2);
  CHECK_STR(pts_string_slice(text, 0, 2)->bytes, "a" HIGH);
  CHECK_STR(pts_string_slice(text, 1, 2)->bytes, HIGH);
  CHECK_STR(pts_string_slice(text, 2, 3)->bytes, LOW);
  CHECK_STR(pts_string_slice(text, 1, 3)->bytes, GRIN);
  CHECK(pts_string_slice(s("é-abc"), 1, TO_END)->flags & PTS_STRING_ASCII);
}

static void substring_swaps_reversed_bounds(void) {
  CHECK_STR(pts_string_substring(s("hello"), 4, 1)->bytes, "ell");
  CHECK_STR(pts_string_substring(s("hello"), -5, 2)->bytes, "he");
  CHECK_STR(pts_string_substring(s("héllo"), 1, TO_END)->bytes, "éllo");
}

static void index_of_finds_code_unit_positions(void) {
  CHECK(pts_string_index_of(s("abcabc"), s("c"), NO_START) == 2);
  CHECK(pts_string_index_of(s("abcabc"), s("c"), 3) == 5);
  CHECK(pts_string_index_of(s("abc"), s("d"), NO_START) == -1);
  CHECK(pts_string_index_of(s("abc"), s(""), 10) == 3);
  CHECK(pts_string_index_of(s("abc"), s("abcd"), NO_START) == -1);
  CHECK(pts_string_index_of(s("é" GRIN "é" GRIN), s("é"), 1) == 3);
  CHECK(pts_string_index_of(s(GRIN "a"), s("a"), 1) == 2);
  CHECK(pts_string_index_of(s("abc"), s("é"), NO_START) == -1);
}

static void search_matches_halves_of_surrogate_pairs(void) {
  CHECK(pts_string_index_of(s(GRIN), s(HIGH), NO_START) == 0);
  CHECK(pts_string_index_of(s("x" GRIN "b"), s(LOW "b"), NO_START) == 2);
  CHECK(pts_string_index_of(s(GRIN GRIN), s(LOW HIGH), NO_START) == 1);
  CHECK(pts_string_includes(s("a" GRIN), s("a" HIGH), NO_START));
  CHECK(!pts_string_includes(s("a" GRIN), s("a" HIGH), 1));
  CHECK(pts_string_ends_with(s(GRIN), s(HIGH), 1));
  CHECK(!pts_string_ends_with(s("a" GRIN), s("a" HIGH), TO_END));
  CHECK(pts_string_starts_with(s(GRIN), s(LOW), 1));
}

static void starts_and_ends_with_honour_positions(void) {
  CHECK(pts_string_starts_with(s("hello"), s("he"), NO_START));
  CHECK(pts_string_starts_with(s("hello"), s("ll"), 2));
  CHECK(!pts_string_starts_with(s("hello"), s("lo!"), 3));
  CHECK(pts_string_starts_with(s("hello"), s(""), 99));
  CHECK(pts_string_ends_with(s("hello"), s("lo"), TO_END));
  CHECK(pts_string_ends_with(s("hello"), s("he"), 2));
  CHECK(!pts_string_ends_with(s("hello"), s("hello!"), TO_END));
  CHECK(pts_string_ends_with(s("héllo"), s("él"), 3));
}

static void trim_removes_javascript_whitespace(void) {
  CHECK_STR(pts_string_trim(s(" \t\n\v\f\r x y \r\n"))->bytes, "x y");
  CHECK_STR(pts_string_trim(s("\xEF\xBB\xBF\xE3\x80\x80" "é\xC2\xA0\xE2\x80\xA8"))->bytes, "é"); /* BOM, ideographic space, NBSP, LS */
  CHECK_STR(pts_string_trim(s("   "))->bytes, "");
  CHECK_STR(pts_string_trim(s("\xE1\xA0\x8E" "x"))->bytes, "\xE1\xA0\x8E" "x"); /* U+180E stopped being a space in Unicode 6.3 */
  CHECK_STR(pts_string_trim_start(s("  a  "))->bytes, "a  ");
  CHECK_STR(pts_string_trim_end(s("  a  "))->bytes, "  a");
  CHECK_STR(pts_string_trim_end(s(GRIN "\xE2\x80\x8A"))->bytes, GRIN); /* hair space */
  pts_string *bare = s("bare");
  CHECK(pts_string_trim(bare) == bare);
}

static void padding_repeats_and_cuts_the_fill(void) {
  CHECK_STR(pts_string_pad_start(s("abc"), 8, s("12"))->bytes, "12121abc");
  CHECK_STR(pts_string_pad_end(s("abc"), 5, s(" "))->bytes, "abc  ");
  CHECK_STR(pts_string_pad_start(s("abc"), 2, s("x"))->bytes, "abc");
  CHECK_STR(pts_string_pad_start(s("abc"), 9, s(""))->bytes, "abc");
  CHECK_STR(pts_string_pad_start(s("abc"), NAN, s("x"))->bytes, "abc");
  pts_string *cut = pts_string_pad_start(s("x"), 2, s(GRIN));
  CHECK_STR(cut->bytes, HIGH "x");
  CHECK(cut->utf16_len == 2);
}

static void padding_joins_surrogate_halves_at_the_seam(void) {
  pts_string *joined = pts_string_pad_end(s("a" HIGH), 3, s(LOW));
  CHECK_STR(joined->bytes, "a" GRIN);
  CHECK(joined->utf16_len == 3);
}

static void repeat_joins_copies(void) {
  pts_string *thrice = pts_string_repeat(s("ab"), 3);
  CHECK_STR(thrice->bytes, "ababab");
  CHECK(thrice->utf16_len == 6 && (thrice->flags & PTS_STRING_ASCII));
  CHECK_STR(pts_string_repeat(s("ab"), 0)->bytes, "");
  CHECK_STR(pts_string_repeat(s("é"), 2.9)->bytes, "éé");
  CHECK_STR(pts_string_repeat(s(""), 1e12)->bytes, "");
  pts_string *seams = pts_string_repeat(s(LOW "x" HIGH), 3);
  CHECK_STR(seams->bytes, LOW "x" GRIN "x" GRIN "x" HIGH);
  CHECK(seams->utf16_len == 9);
}

static void case_mapping_is_unicode_aware(void) {
  CHECK_STR(pts_string_to_upper_case(s("Hello, World 1"))->bytes, "HELLO, WORLD 1");
  CHECK_STR(pts_string_to_lower_case(s("Hello, World 1"))->bytes, "hello, world 1");
  CHECK_STR(pts_string_to_upper_case(s("straße"))->bytes, "STRASSE");
  CHECK_STR(pts_string_to_upper_case(s("ﬃ ŉ"))->bytes, "FFI ʼN");
  CHECK_STR(pts_string_to_lower_case(s("İ"))->bytes, "i\xCC\x87");
  CHECK_STR(pts_string_to_lower_case(s("\xF0\x90\x90\x80"))->bytes, "\xF0\x90\x90\xA8"); /* Deseret, outside the BMP */
  CHECK_STR(pts_string_to_upper_case(s("a" HIGH "é" LOW))->bytes, "A" HIGH "É" LOW);
  pts_string *digits = s("123 !");
  CHECK(pts_string_to_upper_case(digits) == digits);
}

static void final_sigma_lowercases_at_the_end_of_a_word(void) {
  CHECK_STR(pts_string_to_lower_case(s("ΟΔΟΣ"))->bytes, "οδος");
  CHECK_STR(pts_string_to_lower_case(s("Σ"))->bytes, "σ");
  CHECK_STR(pts_string_to_lower_case(s("ΑΣΑ"))->bytes, "ασα");
  CHECK_STR(pts_string_to_lower_case(s("Α.Σ"))->bytes, "α.ς");   /* "." is case-ignorable */
  CHECK_STR(pts_string_to_lower_case(s("ΑΣ.Β"))->bytes, "ασ.β");
  CHECK_STR(pts_string_to_lower_case(s("ΣΣ"))->bytes, "σς");
  CHECK_STR(pts_string_to_lower_case(s("\xCD\x85Σ"))->bytes, "\xCD\x85σ"); /* U+0345 is cased but also ignorable */
}

static void value_to_string_follows_javascript(void) {
  CHECK_STR(pts_value_to_string(pts_value_from_number(-0.0))->bytes, "0");
  CHECK_STR(pts_value_to_string(pts_value_from_number(0.5))->bytes, "0.5");
  CHECK_STR(pts_value_to_string(PTS_VALUE_UNDEFINED)->bytes, "undefined");
  CHECK_STR(pts_value_to_string(PTS_VALUE_FALSE)->bytes, "false");
  CHECK_STR(pts_value_to_string(pts_value_from_i64(INT64_MIN))->bytes, "-9223372036854775808");
  pts_string *text = s("same");
  CHECK(pts_value_to_string(pts_value_from_string(text)) == text);
}

/* JS's StringToNumber: the literal forms Number(s) accepts, NaN for the rest (D-067). */
static void strings_convert_to_numbers_as_number_does(void) {
  CHECK(pts_string_to_number(pts_string_from_cstr("")) == 0);
  CHECK(pts_string_to_number(pts_string_from_cstr(" \t\n ")) == 0);
  CHECK(pts_string_to_number(pts_string_from_cstr("  0x1F  ")) == 31);
  CHECK(pts_string_to_number(pts_string_from_cstr("0b101")) == 5);
  CHECK(pts_string_to_number(pts_string_from_cstr("-Infinity")) == -INFINITY);
  CHECK(pts_string_to_number(pts_string_from_cstr("5.")) == 5);
  CHECK(pts_string_to_number(pts_string_from_cstr(".5e1")) == 5);
  CHECK(isnan(pts_string_to_number(pts_string_from_cstr("1_000"))));
  CHECK(isnan(pts_string_to_number(pts_string_from_cstr("-0x1"))));
  CHECK(isnan(pts_string_to_number(pts_string_from_cstr("inf"))));
  CHECK(isnan(pts_string_to_number(pts_string_from_cstr("1e"))));
  char digits[302];
  memset(digits, '1', 296);
  memcpy(digits + 296, "e-295", 5); /* longer than the stack buffer */
  digits[301] = '\0';
  CHECK(pts_string_to_number(pts_string_from_cstr(digits)) > 1 && pts_string_to_number(pts_string_from_cstr(digits)) < 2);
}

static const char *value_string(pts_value value) {
  return value == PTS_VALUE_UNDEFINED ? "undefined" : ((pts_string *)pts_value_as_object(value))->bytes;
}

static void at_counts_from_the_end(void) {
  CHECK_STR(value_string(pts_string_at(s("abc"), -1)), "c");
  CHECK_STR(value_string(pts_string_at(s("abc"), 1.9)), "b");
  CHECK_STR(value_string(pts_string_at(s("abc"), NO_START)), "a");
  CHECK_STR(value_string(pts_string_at(s("abc"), -4)), "undefined");
  CHECK_STR(value_string(pts_string_at(s("abc"), 3)), "undefined");
  CHECK_STR(value_string(pts_string_at(s("a" GRIN), -1)), LOW);
  CHECK_STR(pts_string_char_at(s("abc"), 5)->bytes, "");
  CHECK_STR(pts_string_char_at(s("abc"), -1)->bytes, "");
  CHECK_STR(pts_string_char_at(s("é" GRIN), 1)->bytes, HIGH);
}

static void code_point_at_joins_pairs(void) {
  CHECK(pts_value_as_number(pts_string_code_point_at(s("é" GRIN), 1)) == 0x1F600);
  CHECK(pts_value_as_number(pts_string_code_point_at(s("é" GRIN), 2)) == 0xDE00);
  CHECK(pts_value_as_number(pts_string_code_point_at(s(HIGH "a"), 0)) == 0xD83D);
  CHECK(pts_value_as_number(pts_string_code_point_at(s("A"), NO_START)) == 65);
  CHECK(pts_string_code_point_at(s("A"), 1) == PTS_VALUE_UNDEFINED);
}

static void last_index_of_searches_backwards(void) {
  CHECK(pts_string_last_index_of(s("abcabc"), s("bc"), NO_START) == 4);
  CHECK(pts_string_last_index_of(s("abcabc"), s("bc"), 3) == 1);
  CHECK(pts_string_last_index_of(s("abcabc"), s("bc"), -5) == -1);
  CHECK(pts_string_last_index_of(s("abcabc"), s(""), NO_START) == 6);
  CHECK(pts_string_last_index_of(s("abcabc"), s(""), 2) == 2);
  CHECK(pts_string_last_index_of(s("ab"), s("abc"), NO_START) == -1);
  CHECK(pts_string_last_index_of(s("é" GRIN "é" GRIN), s("é"), NO_START) == 3);
  CHECK(pts_string_last_index_of(s("é" GRIN "é" GRIN), s(LOW), NO_START) == 5);
  CHECK(pts_string_last_index_of(s("é" GRIN "é" GRIN), s(LOW), 4) == 2);
}

static void substr_takes_a_length(void) {
  CHECK_STR(pts_string_substr(s("abcdef"), 1, 3)->bytes, "bcd");
  CHECK_STR(pts_string_substr(s("abcdef"), -2, TO_END)->bytes, "ef");
  CHECK_STR(pts_string_substr(s("abcdef"), 2, NAN)->bytes, "");
  CHECK_STR(pts_string_substr(s("abcdef"), 4, 10)->bytes, "ef");
  CHECK_STR(pts_string_substr(s("abcdef"), -INFINITY, 2)->bytes, "ab");
  CHECK_STR(pts_string_substr(s("a" GRIN), 1, 1)->bytes, HIGH);
}

static void replace_substitutes_dollar_patterns(void) {
  CHECK_STR(pts_string_replace(s("a-b-c"), s("-"), s("+"))->bytes, "a+b-c");
  CHECK_STR(pts_string_replace(s("a-b"), s("x"), s("+"))->bytes, "a-b");
  CHECK_STR(pts_string_replace(s("abc"), s("b"), s("[$&|$`|$'|$$|$1|$<n>|$]"))->bytes, "a[b|a|c|$|$1|$<n>|$]c");
  CHECK_STR(pts_string_replace(s("abc"), s(""), s("^"))->bytes, "^abc");
  CHECK_STR(pts_string_replace(s("abc"), s("c"), s("$"))->bytes, "ab$");
  CHECK_STR(pts_string_replace_all(s("a-b-c"), s("-"), s("+"))->bytes, "a+b+c");
  CHECK_STR(pts_string_replace_all(s("abc"), s(""), s("."))->bytes, ".a.b.c.");
  CHECK_STR(pts_string_replace_all(s("aaa"), s("aa"), s("b"))->bytes, "ba");
  CHECK_STR(pts_string_replace_all(s("x"), s("y"), s("z"))->bytes, "x");
  CHECK_STR(pts_string_replace_all(s("a.b"), s("."), s("$`$'"))->bytes, "aabb");
  CHECK_STR(pts_string_replace_all(s(GRIN), s(""), s("-"))->bytes, "-" HIGH "-" LOW "-");
  CHECK_STR(pts_string_replace(s(HIGH), s(HIGH), s("x"))->bytes, "x");
}

static void well_formed_strings_have_no_lone_surrogates(void) {
  CHECK(pts_string_is_well_formed(s("abc" GRIN)));
  CHECK(!pts_string_is_well_formed(s("a" HIGH)));
  CHECK(!pts_string_is_well_formed(s(LOW "a")));
  CHECK_STR(pts_string_to_well_formed(s("a" HIGH "b" LOW GRIN))->bytes, "a\xEF\xBF\xBD" "b\xEF\xBF\xBD" GRIN);
  pts_string *text = s("ok");
  CHECK(pts_string_to_well_formed(text) == text);
}

static pts_array *numbers(const double *values, size_t n) { return pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], values, n); }

static void strings_come_from_code_units_and_points(void) {
  double codes[] = {72, 105, 65536 + 33, -1, 0xD83D, 0xDE00, 0xD83D};
  pts_string *text = pts_string_from_char_codes(numbers(codes, 7));
  CHECK_STR(text->bytes, "Hi!\xEF\xBF\xBF" GRIN HIGH);
  CHECK(text->utf16_len == 7);
  double points[] = {0x1F600, 97, 0xD83D, 0xDE00};
  CHECK_STR(pts_string_from_code_points(numbers(points, 4))->bytes, GRIN "a" GRIN);
  CHECK_STR(pts_string_from_char_codes(numbers(NULL, 0))->bytes, "");
}

static int locale_order(const char *a, const char *b) { return (int)pts_string_locale_compare(s(a), s(b)); }

static void locale_compare_follows_the_root_collation(void) {
  CHECK(locale_order("a", "B") == -1);
  CHECK(locale_order("a", "A") == -1);
  CHECK(locale_order("A", "a") == 1);
  CHECK(locale_order("résumé", "resume") == 1);
  CHECK(locale_order("resume", "Resume") == -1);
  CHECK(locale_order("co-op", "coop") == -1);
  CHECK(locale_order("10", "9") == -1);
  CHECK(locale_order("a b", "ab") == -1);
  CHECK(locale_order("", "a") == -1);
  CHECK(locale_order("abc", "abc") == 0);
  CHECK(locale_order("a\x01", "a") == 0); /* controls are ignorable */
  CHECK(locale_order("\xED\x95\x9C", "\xE4\xB8\xAD") == -1); /* Hangul 한 before Han 中 */
  CHECK(locale_order("z", "\xCE\xB1") == -1);                /* Latin before Greek */
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  strings_convert_to_numbers_as_number_does();
  element_reads_one_code_unit_or_undefined();
  ascii_elements_do_not_allocate();
  element_equals_compares_one_unit();
  char_code_at_truncates_and_gives_nan_out_of_range();
  slice_counts_from_the_end_and_clamps();
  slice_cuts_surrogate_pairs_into_lone_surrogates();
  substring_swaps_reversed_bounds();
  index_of_finds_code_unit_positions();
  search_matches_halves_of_surrogate_pairs();
  starts_and_ends_with_honour_positions();
  trim_removes_javascript_whitespace();
  padding_repeats_and_cuts_the_fill();
  padding_joins_surrogate_halves_at_the_seam();
  repeat_joins_copies();
  case_mapping_is_unicode_aware();
  final_sigma_lowercases_at_the_end_of_a_word();
  value_to_string_follows_javascript();
  at_counts_from_the_end();
  code_point_at_joins_pairs();
  last_index_of_searches_backwards();
  substr_takes_a_length();
  replace_substitutes_dollar_patterns();
  well_formed_strings_have_no_lone_surrogates();
  strings_come_from_code_units_and_points();
  locale_compare_follows_the_root_collation();
  return pts_runtime_exit(CHECK_DONE());
}
