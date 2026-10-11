#include "pts.h"

#include "check.h"

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

static void strings_survive_collection(void) {
  pts_string *kept = pts_string_from_cstr("kept across GC");
  for (int i = 0; i < 100000; i++) {
    pts_string_from_cstr("garbage garbage garbage garbage");
  }
  GC_gcollect();
  CHECK_STR(kept->bytes, "kept across GC");
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  ascii_string_is_flagged_and_measured();
  utf16_length_counts_code_units();
  concat_joins_bytes_lengths_and_flags();
  concat_with_empty_returns_other_side();
  static_string_equals_heap_string();
  strings_survive_collection();
  return pts_runtime_exit(CHECK_DONE());
}
