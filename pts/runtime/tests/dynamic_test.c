/* Operations on dynamic values (dynamic.c): JS's conversions, and properties found at run time. */
#include "pts.h"

#include "check.h"

#include <math.h>
#include <stddef.h>

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

static const pts_type number_map_type = {
    .kind = PTS_KIND_MAP,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
    .name = "Map",
    .size = sizeof(pts_map),
    .key = PTS_FIELD_NUMBER,
    .element = PTS_FIELD_NUMBER,
};

/* `in` finds what a library object has on its prototype: its methods, a Map's size (H10b's R6-F2). */
static void in_finds_library_members(void) {
  double elements[] = {10, 20};
  pts_value array = pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, 2));
  CHECK(pts_dynamic_has(array, str("push")) && pts_dynamic_has(array, str("map")) && !pts_dynamic_has(array, str("size")));
  pts_value map = pts_value_from_object(pts_map_new(&number_map_type));
  CHECK(pts_dynamic_has(map, str("size")) && pts_dynamic_has(map, str("get")) && pts_dynamic_has(map, str("forEach")));
  CHECK(!pts_dynamic_has(map, str("push")) && !pts_dynamic_has(map, str("1")));
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

/* A call through `any` whose arguments suspend finds its function first (H08): a builtin's method by name at the call, anything else then. */
static void a_found_callee_is_called_later(void) {
  double elements[] = {10, 20};
  pts_value array = pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, 2));
  pts_value push = str("push");
  pts_value found = pts_dynamic_method_of(array, push);
  CHECK(found == PTS_VALUE_UNDEFINED);
  pts_value thirty = num(30);
  CHECK(pts_dynamic_call_found(array, push, found, pts_string_from_cstr("a.push"), 1, &thirty) == num(3));
  CHECK(pts_dynamic_get(array, num(2)) == num(30));
  CHECK(pts_dynamic_method_of(array, str("length")) == num(3)); /* not a method: the property's value, which the call then finds isn't a function */
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_dynamic_call_found(array, str("length"), num(3), pts_string_from_cstr("a.length"), 0, NULL);
    CHECK(false);
  } else {
    pts_value error = pts_caught(&handler);
    CHECK(pts_value_instance_of(error, &pts_type_builtin_TypeError));
  }
}

static void a_callee_of_null_throws_when_it_is_found(void) {
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_dynamic_method_of(PTS_VALUE_NULL, str("m"));
    CHECK(false);
  } else {
    pts_value error = pts_caught(&handler);
    CHECK(pts_value_instance_of(error, &pts_type_builtin_TypeError));
    CHECK(pts_string_equals(((pts_error *)pts_value_as_object(error))->message, pts_string_from_cstr("Cannot read properties of null (reading 'm')")));
  }
}

typedef struct pair {
  const pts_type *type;
  double first, second;
} pair;

static const pts_field pair_fields[] = {{"0", offsetof(pair, first), PTS_FIELD_NUMBER}, {"1", offsetof(pair, second), PTS_FIELD_NUMBER}};
static const pts_type pair_type = {.kind = PTS_KIND_TUPLE, .name = "Array", .size = sizeof(pair), .field_count = 2, .fields = pair_fields};
static const pts_type value_set_type = {
    .kind = PTS_KIND_SET, .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)), .name = "Set", .size = sizeof(pts_map), .key = PTS_FIELD_VALUE};

/* A spread argument of a call through `any` (H08c): arrays, tuples and typed arrays as they are, spread at the call; anything else's items now. */
static void a_spread_through_any_drains_what_the_call_cannot_spread(void) {
  double elements[] = {10, 20};
  pts_value array = pts_value_from_object(pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], elements, 2));
  const pts_string *text = pts_string_from_cstr("xs");
  CHECK(pts_value_spread_items(array, text) == array);
  pair *tuple = pts_object_new(&pair_type);
  CHECK(pts_value_spread_items(pts_value_from_object(tuple), text) == pts_value_from_object(tuple));
  pts_value typed = pts_value_from_object(pts_typed_array_new(&pts_type_builtin_Float64Array, 2));
  CHECK(pts_value_spread_items(typed, text) == typed);
  pts_value set = pts_value_from_object(pts_set_of_array(&value_set_type, pts_array_of(&pts_array_types[PTS_FIELD_VALUE], (pts_value[]){num(1), num(2)}, 2), false));
  pts_value set_items = pts_value_spread_items(set, text);
  CHECK(pts_value_is_object(set_items) && pts_value_as_object(set_items)->type->kind == PTS_KIND_ARRAY);
  CHECK(pts_array_element((pts_array *)pts_value_as_object(set_items), 1) == num(2));
  pts_array *copy = pts_value_to_array(array, text);
  CHECK(pts_value_from_object(copy) != array && pts_array_length_of(copy) == 2);
  CHECK(pts_array_element(copy, 1) == num(20));
  pts_value items = pts_value_spread_items(str("a\xF0\x9F\x98\x80"), text); /* "a😀": two code points */
  CHECK(pts_value_is_object(items) && pts_value_as_object(items)->type->kind == PTS_KIND_ARRAY);
  CHECK(pts_array_length_of((pts_array *)pts_value_as_object(items)) == 2);
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_value_spread_items(num(5), text);
    CHECK(false);
  } else {
    pts_value error = pts_caught(&handler);
    CHECK(pts_value_instance_of(error, &pts_type_builtin_TypeError));
    CHECK(pts_string_equals(((pts_error *)pts_value_as_object(error))->message, pts_string_from_cstr("xs is not iterable")));
  }
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  to_number_follows_javascript();
  plus_concatenates_when_either_side_is_a_string();
  comparisons_are_false_with_nan();
  arrays_and_strings_answer_indices_and_length();
  in_finds_library_members();
  a_number_element_refuses_a_string();
  integers_fit_exactly_or_not_at_all();
  a_found_callee_is_called_later();
  a_callee_of_null_throws_when_it_is_found();
  a_spread_through_any_drains_what_the_call_cannot_spread();
  return pts_runtime_exit(CHECK_DONE());
}
