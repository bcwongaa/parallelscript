/* Converting dynamic values to the program's types (convert.c): the same value if it fits, a copy of dynamic data, TypeError otherwise. */
#include "pts.h"

#include "check.h"

static const pts_check number_check = {.kind = PTS_CHECK_NUMBER, .slot = PTS_FIELD_NUMBER, .name = "number"};
static const pts_check any_check = {.kind = PTS_CHECK_ANY, .slot = PTS_FIELD_VALUE, .name = "any"};
static const pts_check numbers_check = {.kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "number[]", .element = &number_check};
static const pts_check anys_check = {.kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "any[]", .element = &any_check};
static const pts_check nested_check = {.kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "any[][]", .element = &anys_check};

static pts_array *values_of(size_t count, const pts_value *values) { return pts_array_of(PTS_FIELD_VALUE, values, count); }

static void an_array_of_the_right_kind_is_the_same_array(void) {
  double elements[] = {1, 2};
  pts_array *numbers = pts_array_of(PTS_FIELD_NUMBER, elements, 2);
  CHECK(pts_value_convert(pts_value_from_object(numbers), &numbers_check) == pts_value_from_object(numbers));
}

static void an_array_of_any_is_copied_into_numbers(void) {
  pts_value elements[] = {pts_value_from_number(1), pts_value_from_number(2.5)};
  pts_array *loose = values_of(2, elements);
  pts_value converted = pts_value_convert(pts_value_from_object(loose), &numbers_check);
  pts_array *copy = (pts_array *)pts_value_as_object(converted);
  CHECK(copy != loose && copy->type->element == PTS_FIELD_NUMBER);
  CHECK(pts_array_element(copy, 1) == pts_value_from_number(2.5));
}

static void an_element_that_doesnt_fit_throws(void) {
  pts_value elements[] = {pts_value_from_number(1), pts_value_from_string(pts_string_from_cstr("2"))};
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_value_convert(pts_value_from_object(values_of(2, elements)), &numbers_check);
    CHECK(false);
  } else {
    CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError));
  }
}

static void a_self_containing_array_converts_without_looping(void) {
  pts_value placeholder = PTS_VALUE_UNDEFINED;
  pts_array *outer = values_of(1, &placeholder);
  pts_value self = pts_value_from_object(outer);
  pts_array_set(outer, 0, &self); /* outer[0] is outer */
  pts_array *copy = (pts_array *)pts_value_as_object(pts_value_convert(self, &nested_check));
  CHECK(copy != outer && copy->type->element == PTS_FIELD_OBJECT); /* any[][] holds pointers where any[] holds pts_values */
  CHECK(pts_array_element(copy, 0) == self);                        /* an any[] fits as it is */
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  an_array_of_the_right_kind_is_the_same_array();
  an_array_of_any_is_copied_into_numbers();
  an_element_that_doesnt_fit_throws();
  a_self_containing_array_converts_without_looping();
  return pts_runtime_exit(CHECK_DONE());
}
