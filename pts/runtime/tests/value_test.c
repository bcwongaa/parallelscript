#include "pts.h"

#include <math.h>

#include "check.h"

static void numbers_round_trip(void) {
  double samples[] = {0.0, -0.0, 1.0, -1.5, 1e308, -1e-308, 5e-324, INFINITY, -INFINITY};
  for (size_t i = 0; i < sizeof samples / sizeof samples[0]; i++) {
    pts_value v = pts_value_from_number(samples[i]);
    CHECK(pts_value_is_number(v));
    CHECK(!pts_value_is_object(v));
    double back = pts_value_as_number(v);
    CHECK(memcmp(&back, &samples[i], sizeof back) == 0);
  }
}

static void nan_is_canonicalised(void) {
  double negative_nan = -NAN;
  pts_value v = pts_value_from_number(negative_nan);
  CHECK(pts_value_is_number(v));
  CHECK(isnan(pts_value_as_number(v)));
}

static void constants_are_neither_numbers_nor_objects(void) {
  pts_value constants[] = {PTS_VALUE_UNDEFINED, PTS_VALUE_NULL, PTS_VALUE_TRUE, PTS_VALUE_FALSE};
  for (size_t i = 0; i < sizeof constants / sizeof constants[0]; i++) {
    CHECK(!pts_value_is_number(constants[i]));
    CHECK(!pts_value_is_object(constants[i]));
  }
  CHECK(pts_value_from_bool(true) == PTS_VALUE_TRUE);
  CHECK(pts_value_from_bool(false) == PTS_VALUE_FALSE);
}

static void objects_are_stored_as_raw_pointers(void) {
  pts_string *s = pts_string_from_cstr("boxed");
  pts_value v = pts_value_from_string(s);
  CHECK(pts_value_is_object(v));
  CHECK(!pts_value_is_number(v));
  CHECK(pts_value_as_object(v) == (pts_object *)s);
  CHECK(pts_value_as_object(v)->type == &pts_string_type);
}

/* The GC must see a pts_value as a reference: a value is the object's address. */
static void boxed_object_survives_collection(void) {
  pts_value *slot = GC_MALLOC(sizeof(pts_value));
  *slot = pts_value_from_string(pts_string_from_cstr("only reachable through a pts_value"));
  for (int i = 0; i < 100000; i++) {
    pts_string_from_cstr("garbage garbage garbage garbage");
  }
  GC_gcollect();
  pts_string *s = (pts_string *)pts_value_as_object(*slot);
  CHECK_STR(s->bytes, "only reachable through a pts_value");
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  numbers_round_trip();
  nan_is_canonicalised();
  constants_are_neither_numbers_nor_objects();
  objects_are_stored_as_raw_pointers();
  boxed_object_survives_collection();
  return pts_runtime_exit(CHECK_DONE());
}
