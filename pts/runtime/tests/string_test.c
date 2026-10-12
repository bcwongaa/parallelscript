#include "pts.h"

#include "check.h"

#include <pthread.h>
#include <stdio.h>

static void ascii_string_is_flagged_and_measured(void) {
  pts_string *s = pts_string_from_cstr("hello");
  CHECK(s->flags & PTS_STRING_ASCII);
  CHECK(s->byte_len == 5);
  CHECK(s->utf16_len == 5);
  CHECK_STR(s->bytes, "hello");
}

static void utf16_length_counts_code_units(void) {
  /* "é" is 2 UTF-8 bytes and 1 UTF-16 unit; "😀" is 4 bytes and a surrogate pair. */
  pts_string *s = pts_string_from_cstr("é😀a");
  CHECK(!(s->flags & PTS_STRING_ASCII));
  CHECK(s->byte_len == 7);
  CHECK(s->utf16_len == 4);
}

static void concat_joins_bytes_lengths_and_flags(void) {
  pts_string *ascii = pts_string_from_cstr("ab");
  pts_string *wide = pts_string_from_cstr("é");
  pts_string *joined = pts_string_concat(ascii, wide);
  CHECK_STR(joined->bytes, "abé");
  CHECK(joined->byte_len == 4);
  CHECK(joined->utf16_len == 3);
  CHECK(!(joined->flags & PTS_STRING_ASCII));
  CHECK(pts_string_concat(ascii, ascii)->flags & PTS_STRING_ASCII);
}

static void concat_with_empty_returns_other_side(void) {
  pts_string *empty = pts_string_from_cstr("");
  pts_string *s = pts_string_from_cstr("x");
  CHECK(pts_string_concat(empty, s) == s);
  CHECK(pts_string_concat(s, empty) == s);
}

PTS_STATIC_STRING(static_hello, PTS_STRING_ASCII, 5, 5, "hello");

static void static_string_equals_heap_string(void) {
  CHECK(pts_string_equals(static_hello, pts_string_from_cstr("hello")));
  CHECK(!pts_string_equals(static_hello, pts_string_from_cstr("hellO")));
  CHECK(!pts_string_equals(static_hello, pts_string_from_cstr("hell")));
}

/* "\uD83D" and "\uDE00" as the scanner encodes lone surrogates (WTF-8). */
static const char HIGH[] = "\xED\xA0\xBD", LOW[] = "\xED\xB8\x80";

static void surrogate_halves_join_into_one_code_point(void) {
  pts_string *joined = pts_string_concat(pts_string_from_cstr(HIGH), pts_string_from_cstr(LOW));
  CHECK_STR(joined->bytes, "\xF0\x9F\x98\x80"); /* U+1F600 */
  CHECK(joined->byte_len == 4);
  CHECK(joined->utf16_len == 2);
  pts_string *apart = pts_string_concat(pts_string_from_cstr(LOW), pts_string_from_cstr(HIGH));
  CHECK(apart->byte_len == 6); /* low then high is no pair */
  pts_string *prefixed = pts_string_concat(pts_string_from_cstr("a\xED\xA0\xBD"), pts_string_from_cstr("\xED\xB8\x80" "b"));
  CHECK_STR(prefixed->bytes, "a\xF0\x9F\x98\x80" "b");
  CHECK(prefixed->utf16_len == 4);
}

/* Strings known to be valid UTF-8 (no lone surrogate) let PCRE2 skip its check of the subject. */
static void strings_without_lone_surrogates_are_known_well_formed(void) {
  CHECK(pts_string_known_well_formed(pts_string_from_cstr("plain")));
  pts_string *wide = pts_string_from_cstr("é😀 and more than eight bytes of ASCII");
  CHECK(wide->flags & PTS_STRING_WELL_FORMED);
  CHECK(wide->utf16_len == 38);
  CHECK(!pts_string_known_well_formed(pts_string_from_cstr("a" "\xED\xA0\xBD" "b")));
  CHECK(!pts_string_known_well_formed(pts_string_from_cstr(LOW)));
  CHECK(pts_string_known_well_formed(pts_string_concat(pts_string_from_cstr("ab"), wide)));
  CHECK(!pts_string_known_well_formed(pts_string_concat(wide, pts_string_from_cstr(HIGH))));
  CHECK(!pts_string_known_well_formed(pts_string_concat(pts_string_from_cstr(HIGH), pts_string_from_cstr(LOW)))); /* a pair joined, but not known to be */
  CHECK(pts_string_known_well_formed(pts_string_repeat(wide, 3)));
  pts_string *decoded = pts_string_from_utf8_lossy("caf\xC3\xA9 au lait, s'il vous pla\xC3\xAEt", 31);
  CHECK(decoded->flags == PTS_STRING_WELL_FORMED);
  CHECK(decoded->utf16_len == 29);
  pts_string *ascii = pts_string_from_utf8_lossy("only ASCII here, and long", 25);
  CHECK(ascii->flags == PTS_STRING_ASCII);
  CHECK(ascii->utf16_len == 25);
}

static void comparison_follows_utf16_code_units(void) {
  CHECK(pts_string_compare(pts_string_from_cstr("abc"), pts_string_from_cstr("abd")) < 0);
  CHECK(pts_string_compare(pts_string_from_cstr("ab"), pts_string_from_cstr("abc")) < 0);
  CHECK(pts_string_compare(pts_string_from_cstr("b"), pts_string_from_cstr("abc")) > 0);
  CHECK(pts_string_compare(pts_string_from_cstr("abc"), pts_string_from_cstr("abc")) == 0);
  CHECK(pts_string_compare(pts_string_from_cstr("Z"), pts_string_from_cstr("a")) < 0);
  /* U+1F600 is the units D83D DE00, which sort before U+FFFF, unlike in UTF-8 byte order. */
  CHECK(pts_string_compare(pts_string_from_cstr("\xF0\x9F\x98\x80"), pts_string_from_cstr("\xEF\xBF\xBF")) < 0);
  CHECK(pts_string_compare(pts_string_from_cstr("é"), pts_string_from_cstr("e")) > 0);
  CHECK(pts_string_compare(pts_string_from_cstr("é"), pts_string_from_cstr("éa")) < 0);
}

static void sixty_four_bit_integers_print_exactly(void) {
  CHECK_STR(pts_string_from_i64(INT64_MIN)->bytes, "-9223372036854775808");
  CHECK_STR(pts_string_from_u64(UINT64_MAX)->bytes, "18446744073709551615");
  CHECK(pts_string_from_i64(42)->flags & PTS_STRING_ASCII);
}

static void output_replaces_lone_surrogates(void) {
  char *buf = NULL;
  size_t size = 0;
  FILE *out = open_memstream(&buf, &size);
  pts_string_write_for_output(pts_string_from_cstr("a\xED\xA0\xBD" "b\xF0\x9F\x98\x80" "c\xED\xB8\x80"), out);
  fclose(out);
  CHECK_STR(buf, "a\xEF\xBF\xBD" "b\xF0\x9F\x98\x80" "c\xEF\xBF\xBD");
  free(buf);
}

static void strings_survive_collection(void) {
  pts_string *kept = pts_string_from_cstr("kept across GC");
  for (int i = 0; i < 100000; i++) {
    pts_string_from_cstr("garbage garbage garbage garbage");
  }
  GC_gcollect();
  CHECK_STR(kept->bytes, "kept across GC");
}

static bool equals_cstr(const pts_string *string, const char *expected) {
  return pts_string_equals(string, pts_string_from_cstr(expected));
}

/* long_string is a string long enough for concatenations onto it to go to an append buffer. */
static pts_string *long_string(void) {
  pts_string *string = pts_string_from_cstr("");
  for (int i = 0; i < 10; i++) {
    string = pts_string_concat(string, pts_string_from_cstr("0123456789"));
  }
  return string;
}

static void appending_leaves_earlier_strings_unchanged(void) {
  pts_string *base = long_string();
  pts_string *x = pts_string_concat(base, pts_string_from_cstr("x"));
  pts_string *xy = pts_string_concat(x, pts_string_from_cstr("y"));
  pts_string *xz = pts_string_concat(x, pts_string_from_cstr("z")); /* x no longer ends the buffer: a copy */
  CHECK(xy->bytes == x->bytes && xz->bytes != x->bytes);
  char digits[101], expected[128];
  snprintf(digits, sizeof digits, "%.*s", (int)base->byte_len, base->bytes);
  CHECK(base->byte_len == 100 && equals_cstr(base, digits));
  snprintf(expected, sizeof expected, "%sx", digits);
  CHECK(equals_cstr(x, expected));
  snprintf(expected, sizeof expected, "%sxy", digits);
  CHECK(equals_cstr(xy, expected) && xy->utf16_len == 102 && (xy->flags & PTS_STRING_ASCII));
  snprintf(expected, sizeof expected, "%sxz", digits);
  CHECK(equals_cstr(xz, expected));
}

static void building_a_string_piece_by_piece_reuses_its_buffer(void) {
  pts_string *built = long_string();
  pts_string *first = built;
  for (int i = 0; i < 100000; i++) {
    built = pts_string_concat(built, pts_string_from_cstr("é"));
  }
  CHECK(built->byte_len == 100 + 200000 && built->utf16_len == 100 + 100000 && !(built->flags & PTS_STRING_ASCII));
  CHECK(built->bytes[100] == '\xC3' && built->bytes[built->byte_len - 1] == '\xA9');
  pts_string *doubled = pts_string_concat(first, first);
  CHECK(doubled->byte_len == 200 && memcmp(doubled->bytes, doubled->bytes + 100, 100) == 0);
}

enum { RACING_THREADS = 4, RACING_APPENDS = 20000 };

static pts_string *shared_base;

/* Each thread appends its own letter to the shared string and to what it built, and checks every result. */
static void *append_racing(void *arg) {
  char letter[2] = {(char)('a' + (intptr_t)arg), 0};
  pts_string *suffix = pts_string_from_cstr(letter);
  pts_string *built = shared_base;
  bool intact = true;
  for (int i = 0; i < RACING_APPENDS; i++) {
    pts_string *once = pts_string_concat(shared_base, suffix);
    intact = intact && once->byte_len == 101 && once->bytes[100] == letter[0] && memcmp(once->bytes, shared_base->bytes, 100) == 0;
    built = pts_string_concat(built, suffix);
  }
  for (uint32_t i = 100; i < built->byte_len; i++) {
    intact = intact && built->bytes[i] == letter[0];
  }
  return (void *)(intptr_t)(intact && built->byte_len == 100 + RACING_APPENDS);
}

static void racing_appends_never_mix_bytes(void) {
  shared_base = long_string();
  pthread_t threads[RACING_THREADS];
  for (int i = 0; i < RACING_THREADS; i++) {
    CHECK(pthread_create(&threads[i], NULL, append_racing, (void *)(intptr_t)i) == 0);
  }
  for (int i = 0; i < RACING_THREADS; i++) {
    void *intact = NULL;
    CHECK(pthread_join(threads[i], &intact) == 0);
    CHECK(intact == (void *)1);
  }
}

/* Decodes len bytes leniently and checks the result's UTF-8. */
static void check_lossy(const char *bytes, size_t len, const char *expected) {
  pts_string *s = pts_string_from_utf8_lossy(bytes, len);
  CHECK(s->byte_len == strlen(expected));
  CHECK_STR(s->bytes, expected);
}

static void lossy_decoding_replaces_each_invalid_sequence(void) {
  check_lossy("plain é😀", 12, "plain é😀");
  check_lossy("\xFF", 1, "\xEF\xBF\xBD");
  check_lossy("a\x80\x80" "b", 4, "a\xEF\xBF\xBD\xEF\xBF\xBD" "b");                         /* lone continuation bytes, one each */
  check_lossy("\xE2\x82" "A", 3, "\xEF\xBF\xBD" "A");                                        /* a truncated sequence is one */
  check_lossy("x\xE2\x82", 3, "x\xEF\xBF\xBD");                                        /* also at the end */
  check_lossy("\xED\xA0\x80", 3, "\xEF\xBF\xBD\xEF\xBF\xBD\xEF\xBF\xBD");             /* a surrogate's encoding is invalid */
  check_lossy("\xC0\xAF", 2, "\xEF\xBF\xBD\xEF\xBF\xBD");                               /* an overlong form */
  check_lossy("\xF4\x90\x80\x80", 4, "\xEF\xBF\xBD\xEF\xBF\xBD\xEF\xBF\xBD\xEF\xBF\xBD"); /* above U+10FFFF */
  check_lossy("\xF0\x9F\x98", 3, "\xEF\xBF\xBD");
}

static void encoding_replaces_lone_surrogates(void) {
  pts_string *pair = pts_string_concat(pts_string_from_cstr(HIGH), pts_string_from_cstr(LOW));
  size_t len;
  const char *bytes = pts_string_to_utf8(pair, &len);
  CHECK(bytes == pair->bytes && len == 4); /* a pair is already UTF-8 */
  pts_string *lone = pts_string_concat(pts_string_from_cstr("a"), pts_string_from_cstr(LOW));
  bytes = pts_string_to_utf8(lone, &len);
  CHECK(len == 4 && memcmp(bytes, "a\xEF\xBF\xBD", 4) == 0);
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  lossy_decoding_replaces_each_invalid_sequence();
  encoding_replaces_lone_surrogates();
  ascii_string_is_flagged_and_measured();
  utf16_length_counts_code_units();
  concat_joins_bytes_lengths_and_flags();
  concat_with_empty_returns_other_side();
  static_string_equals_heap_string();
  surrogate_halves_join_into_one_code_point();
  strings_without_lone_surrogates_are_known_well_formed();
  comparison_follows_utf16_code_units();
  sixty_four_bit_integers_print_exactly();
  output_replaces_lone_surrogates();
  strings_survive_collection();
  appending_leaves_earlier_strings_unchanged();
  building_a_string_piece_by_piece_reuses_its_buffer();
  racing_appends_never_mix_bytes();
  return pts_runtime_exit(CHECK_DONE());
}
