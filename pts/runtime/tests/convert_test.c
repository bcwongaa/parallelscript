/* Converting dynamic values to the program's types (convert.c): the same value if it fits, a copy of dynamic data, TypeError otherwise. */
#include "pts.h"

#include "check.h"

static const pts_check number_check = {.kind = PTS_CHECK_NUMBER, .slot = PTS_FIELD_NUMBER, .name = "number"};
static const pts_check any_check = {.kind = PTS_CHECK_ANY, .slot = PTS_FIELD_VALUE, .name = "any"};
static const pts_check numbers_check = {
    .kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "number[]", .element = &number_check, .type = &pts_array_types[PTS_FIELD_NUMBER]};
static const pts_check anys_check = {
    .kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "any[]", .element = &any_check, .type = &pts_array_types[PTS_FIELD_VALUE]};
/* any[][]: an array of objects, which the program describes (D-072). */
static const pts_type nested_type = {
    .kind = PTS_KIND_ARRAY,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_array, buffer)),
    .name = "Array",
    .size = sizeof(pts_array),
    .element = PTS_FIELD_OBJECT,
    .check = &anys_check,
};
static const pts_check nested_check = {.kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "any[][]", .element = &anys_check, .type = &nested_type};

static pts_array *values_of(size_t count, const pts_value *values) { return pts_array_of(&pts_array_types[PTS_FIELD_VALUE], values, count); }

static void an_array_of_the_right_kind_is_the_same_array(void) {
  double elements[] = {1, 2};
  pts_array *numbers = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, 2);
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
  CHECK(copy != outer && copy->type == &nested_type); /* any[][] holds pointers where any[] holds pts_values */
  CHECK(pts_array_element(copy, 0) == self);                        /* an any[] fits as it is */
}

/* [number, any?]: a tuple whose second element is optional (D-100). */
typedef struct {
  pts_object header;
  double first;
  pts_value second;
} optional_pair;
static const pts_field optional_pair_fields[] = {
    {"0", offsetof(optional_pair, first), PTS_FIELD_NUMBER, NULL},
    {"1", offsetof(optional_pair, second), PTS_FIELD_VALUE, NULL},
};
static const pts_type optional_pair_type = {
    .kind = PTS_KIND_TUPLE,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(optional_pair, second)),
    .name = "Array",
    .size = sizeof(optional_pair),
    .field_count = 2,
    .fields = optional_pair_fields,
};
static const pts_check_property optional_pair_elements[] = {
    {"0", &number_check, false, offsetof(optional_pair, first)},
    {"1", &any_check, true, offsetof(optional_pair, second)},
};
static const pts_check optional_pair_check = {
    .kind = PTS_CHECK_TUPLE, .slot = PTS_FIELD_OBJECT, .name = "[number, any?]", .type = &optional_pair_type, .count = 2, .properties = optional_pair_elements};

static bool converts_to_optional_pair(size_t count, const pts_value *values, pts_value *out) {
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    *out = pts_value_convert(pts_value_from_object(values_of(count, values)), &optional_pair_check);
    pts_try_leave(&handler);
    return true;
  }
  CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError));
  return false;
}

/* An array shorter than a tuple type by its optional elements converts, leaving them out, as `const t: [number, string?] = [1]` (H05 review round 6). */
static void an_array_without_optional_elements_is_copied_into_a_tuple(void) {
  pts_value elements[] = {pts_value_from_number(1), pts_value_from_number(2), pts_value_from_number(3)};
  pts_value out;
  bool converted = converts_to_optional_pair(1, elements, &out);
  CHECK(converted);
  if (!converted) {
    return;
  }
  CHECK(pts_value_as_object(out)->type == &optional_pair_type && pts_tuple_length(pts_value_as_object(out)) == 1);
  CHECK(((optional_pair *)pts_value_as_object(out))->first == 1);
  CHECK(converts_to_optional_pair(2, elements, &out) && pts_tuple_length(pts_value_as_object(out)) == 2);
  CHECK(!converts_to_optional_pair(0, elements, &out)); /* the required element is missing */
  CHECK(!converts_to_optional_pair(3, elements, &out)); /* one too many */
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  an_array_of_the_right_kind_is_the_same_array();
  an_array_of_any_is_copied_into_numbers();
  an_element_that_doesnt_fit_throws();
  a_self_containing_array_converts_without_looping();
  an_array_without_optional_elements_is_copied_into_a_tuple();
  return pts_runtime_exit(CHECK_DONE());
}
