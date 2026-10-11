/* Operations on dynamic values (dynamic.c): JS's conversions, and properties found at run time. */
#include "pts.h"

#include "check.h"

#include <math.h>

static pts_value str(const char *text) { return pts_value_from_string(pts_string_from_cstr(text)); }
static pts_value num(double n) { return pts_value_from_number(n); }

static void to_number_follows_javascript(void) {
  CHECK(pts_value_to_number(str(" 42 ")) == 42);
  CHECK(isnan(pts_value_to_number(str("4x"))));
  CHECK(pts_value_to_number(str("")) == 0);
  CHECK(pts_value_to_number(PTS_VALUE_NULL) == 0);
  CHECK(isnan(pts_value_to_number(PTS_VALUE_UNDEFINED)));
  CHECK(pts_value_to_number(PTS_VALUE_TRUE) == 1);
  double element = 7;
  CHECK(pts_value_to_number(pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], &element, 1))) == 7); /* "7" */
}

static void plus_concatenates_when_either_side_is_a_string(void) {
  pts_value sum = pts_value_add(num(1), str("2"));
  CHECK(pts_value_is_string(sum) && pts_string_equals(pts_value_to_string(sum), pts_string_from_cstr("12")));
  CHECK(pts_value_add(num(1), PTS_VALUE_TRUE) == num(2));
  CHECK(isnan(pts_value_as_number(pts_value_add(num(1), PTS_VALUE_UNDEFINED))));
}

static void comparisons_are_false_with_nan(void) {
  CHECK(pts_value_less(str("10"), str("9")));  /* as strings */
  CHECK(!pts_value_less(str("10"), num(9)));   /* as numbers */
  CHECK(!pts_value_less(num(NAN), num(1)) && !pts_value_less_equal(num(NAN), num(1)));
  CHECK(pts_value_less_equal(PTS_VALUE_NULL, num(0)));
}

static void arrays_and_strings_answer_indices_and_length(void) {
  double elements[] = {10, 20};
  pts_value array = pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, 2));
  CHECK(pts_dynamic_get(array, str("length")) == num(2));
  CHECK(pts_dynamic_get(array, num(1)) == num(20));
  CHECK(pts_dynamic_get(array, str("1")) == num(20));
  CHECK(pts_dynamic_get(array, str("01")) == PTS_VALUE_UNDEFINED);
  CHECK(pts_dynamic_get(array, num(5)) == PTS_VALUE_UNDEFINED);
  pts_dynamic_set(array, num(2), num(30));
  CHECK(pts_dynamic_get(array, str("length")) == num(3));
  CHECK(pts_dynamic_get(str("abc"), str("length")) == num(3));
  CHECK(pts_dynamic_has(array, str("2")) && !pts_dynamic_has(array, str("3")));
}

static void a_number_element_refuses_a_string(void) {
  double elements[] = {1};
  pts_value array = pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, 1));
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_dynamic_set(array, num(0), str("one"));
    CHECK(false);
  } else {
    CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError));
  }
  CHECK(pts_dynamic_get(array, num(0)) == num(1));
}

static void integers_fit_exactly_or_not_at_all(void) {
  CHECK(pts_value_is_integer_of(num(255), PTS_FIELD_U8));
  CHECK(!pts_value_is_integer_of(num(256), PTS_FIELD_U8));
  CHECK(!pts_value_is_integer_of(num(-1), PTS_FIELD_U32));
  CHECK(!pts_value_is_integer_of(num(1.5), PTS_FIELD_I64));
  CHECK(pts_value_is_integer_of(num(-128), PTS_FIELD_I8));
  CHECK(!pts_value_is_integer_of(str("1"), PTS_FIELD_I32));
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  to_number_follows_javascript();
  plus_concatenates_when_either_side_is_a_string();
  comparisons_are_false_with_nan();
  arrays_and_strings_answer_indices_and_length();
  a_number_element_refuses_a_string();
  integers_fit_exactly_or_not_at_all();
  return pts_runtime_exit(CHECK_DONE());
}
