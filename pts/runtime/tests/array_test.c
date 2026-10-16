/* Arrays: growth, the operations at both ends, boxing, and threads racing on one array. */
#include "pts.h"

#include "check.h"

#include <pthread.h>

enum { THREADS = 4, PUSHES = 20000 };

static pts_array *numbers(size_t count) {
  pts_array *array = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], NULL, 0);
  for (size_t i = 0; i < count; i++) {
    double value = (double)i;
    pts_array_push(array, &value, 1);
  }
  return array;
}

static void pushing_grows_the_buffer_and_keeps_the_elements(void) {
  pts_array *array = numbers(1000);
  CHECK(pts_array_length(array) == 1000);
  CHECK(array->buffer->capacity >= 1000);
  for (size_t i = 0; i < 1000; i++) {
    CHECK(PTS_ARRAY_GET(double, array, (double)i) == (double)i);
  }
}

static void indices_out_of_range_have_no_slot(void) {
  pts_array *array = numbers(3);
  CHECK(pts_array_slot_or_null(array, 2, sizeof(double)) != NULL);
  CHECK(pts_array_slot_or_null(array, 3, sizeof(double)) == NULL);
  CHECK(pts_array_slot_or_null(array, -1, sizeof(double)) == NULL);
  CHECK(pts_array_slot_or_null(array, 0.5, sizeof(double)) == NULL);
  CHECK(pts_array_element(array, 7) == PTS_VALUE_UNDEFINED);
  CHECK(pts_value_as_number(pts_array_element(array, 1)) == 1);
}

static void both_ends_add_and_remove_in_order(void) {
  pts_array *array = numbers(3); /* 0 1 2 */
  double front[] = {-2, -1};
  CHECK(pts_array_unshift(array, front, 2) == 5); /* -2 -1 0 1 2 */
  double out;
  pts_array_shift(array, &out);
  CHECK(out == -2);
  pts_array_pop(array, &out);
  CHECK(out == 2);
  CHECK(pts_array_length(array) == 3);
  CHECK(PTS_ARRAY_GET(double, array, 0) == -1 && PTS_ARRAY_GET(double, array, 2) == 1);
  CHECK(pts_array_set_length(array, 1) == 1);
  CHECK(pts_value_as_number(pts_array_pop_value(array)) == -1);
  CHECK(pts_array_pop_value(array) == PTS_VALUE_UNDEFINED);
  CHECK(pts_array_shift_value(array) == PTS_VALUE_UNDEFINED);
}

static void elements_of_every_kind_box_as_their_values(void) {
  pts_string *word = pts_string_from_cstr("word");
  pts_array *strings = pts_array_of(&pts_array_types[PTS_FIELD_STRING], &word, 1);
  CHECK(pts_array_element(strings, 0) == pts_value_from_string(word));
  bool flags[] = {true, false};
  pts_array *bools = pts_array_of(&pts_array_types[PTS_FIELD_BOOL], flags, 2);
  CHECK(pts_array_element(bools, 0) == PTS_VALUE_TRUE && pts_array_element(bools, 1) == PTS_VALUE_FALSE);
  uint8_t bytes[] = {255, 7};
  pts_array *small = pts_array_of(&pts_array_types[PTS_FIELD_U8], bytes, 2);
  CHECK(pts_value_as_number(pts_array_element(small, 0)) == 255);
  uint8_t more = 9;
  pts_array_push(small, &more, 1);
  CHECK(PTS_ARRAY_GET(uint8_t, small, 2) == 9 && pts_array_length(small) == 3);
}

static void a_cycle_joins_as_empty(void) {
  pts_array *outer = pts_array_of(&pts_array_types[PTS_FIELD_OBJECT], NULL, 0);
  pts_array *inner = numbers(2);
  pts_array_push(outer, &inner, 1);
  pts_array_push(outer, &outer, 1);
  CHECK_STR(pts_array_join(outer, NULL)->bytes, "0,1,");
}

static void splice_clamps_and_inserts(void) {
  pts_array *array = numbers(5); /* 0 1 2 3 4 */
  double items[] = {10, 11, 12};
  pts_array *removed = pts_array_splice(array, -2, 100, items, 3);
  CHECK(pts_array_length(removed) == 2 && PTS_ARRAY_GET(double, removed, 0) == 3);
  CHECK(pts_array_length(array) == 6 && PTS_ARRAY_GET(double, array, 3) == 10 && PTS_ARRAY_GET(double, array, 5) == 12);
  removed = pts_array_splice(array, 1, -5, NULL, 0);
  CHECK(pts_array_length(removed) == 0 && pts_array_length(array) == 6);
}

static void sort_keeps_equal_keys_in_order(void) {
  pts_string *words[] = {pts_string_from_cstr("b"), pts_string_from_cstr("a"), pts_string_from_cstr("b"), pts_string_from_cstr("a")};
  pts_array *array = pts_array_of(&pts_array_types[PTS_FIELD_STRING], words, 4);
  pts_array_sort(array);
  CHECK(PTS_ARRAY_GET(pts_string *, array, 0) == words[1] && PTS_ARRAY_GET(pts_string *, array, 1) == words[3]);
  CHECK(PTS_ARRAY_GET(pts_string *, array, 2) == words[0] && PTS_ARRAY_GET(pts_string *, array, 3) == words[2]);
}

static pts_array *shared;

/* push_many pushes PUSHES numbers onto the shared array while other threads do too. */
static void *push_many(void *arg) {
  for (int i = 0; i < PUSHES; i++) {
    double value = 1;
    pts_array_push(shared, &value, 1);
  }
  return arg;
}

/* read_while_pushing reads every element in range while the others push: each is 1, or 0 in a slot a lost push left. */
static void *read_while_pushing(void *arg) {
  bool fine = true;
  for (int round = 0; round < 200; round++) {
    size_t length = pts_array_length_of(shared);
    for (size_t i = 0; i < length; i += 7) {
      const double *slot = pts_array_slot_or_null(shared, (double)i, sizeof(double));
      fine = fine && (slot == NULL || *slot == 1 || *slot == 0);
    }
  }
  return (void *)(intptr_t)fine;
}

static void racing_pushes_lose_elements_but_stay_in_bounds(void) {
  shared = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], NULL, 0);
  pthread_t pushers[THREADS], reader;
  for (int i = 0; i < THREADS; i++) {
    CHECK(pthread_create(&pushers[i], NULL, push_many, NULL) == 0);
  }
  CHECK(pthread_create(&reader, NULL, read_while_pushing, NULL) == 0);
  for (int i = 0; i < THREADS; i++) {
    CHECK(pthread_join(pushers[i], NULL) == 0);
  }
  void *fine = NULL;
  CHECK(pthread_join(reader, &fine) == 0);
  CHECK(fine == (void *)1);
  size_t length = pts_array_length_of(shared);
  CHECK(length > 0 && length <= (size_t)THREADS * PUSHES);
  CHECK(length <= shared->buffer->capacity);
}

/* A comparator of numbers by their integer part, so 1.1 and 1.2 compare equal. */
static double by_integer_part(pts_closure *compare, double a, double b) {
  (void)compare;
  return (double)((int)a - (int)b);
}

static double call_number_comparator(pts_closure *compare, const void *a, const void *b) {
  return ((double (*)(pts_closure *, double, double))compare->code)(compare, *(const double *)a, *(const double *)b);
}

static void sort_with_a_comparator_is_stable(void) {
  static const pts_type closure_type = {.kind = PTS_KIND_FUNCTION, .name = "byIntegerPart", .size = sizeof(pts_closure)};
  pts_closure compare = {&closure_type, (pts_function)by_integer_part};
  double values[] = {3.1, 1.1, 2.1, 1.2, 3.2, 1.3};
  pts_array *array = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], values, 6);
  CHECK(pts_array_sort_with(array, &compare, call_number_comparator) == array);
  double sorted[] = {1.1, 1.2, 1.3, 2.1, 3.1, 3.2};
  for (size_t i = 0; i < 6; i++) {
    CHECK(PTS_ARRAY_GET(double, array, (double)i) == sorted[i]);
  }
}

/* strings_with_a_hole is ["a", <hole>, "c"]: a racing push published the length before its element (D-038). */
static pts_array *strings_with_a_hole(void) {
  pts_string *items[] = {pts_string_from_cstr("a"), NULL, pts_string_from_cstr("c")};
  return pts_array_of(&pts_array_types[PTS_FIELD_STRING], items, 3);
}

static void loads_report_holes_and_missing_elements(void) {
  pts_array *array = strings_with_a_hole();
  pts_string *element = NULL;
  CHECK(pts_array_load(array, 0, &element) && pts_string_equals(element, pts_string_from_cstr("a")));
  CHECK(!pts_array_load(array, 1, &element) && !pts_array_load(array, 3, &element) && !pts_array_load(array, -1, &element));
}

static void a_set_of_an_array_leaves_out_holes(void) {
  static const pts_type string_set_type = {
      .kind = PTS_KIND_SET,
      .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_map, table)),
      .name = "Set",
      .size = sizeof(pts_map),
      .key = PTS_FIELD_STRING,
  };
  CHECK(pts_map_size(pts_set_of_array(&string_set_type, strings_with_a_hole())) == 2);
}

/* A splice racing the first push can publish an empty buffer under a length of 1 (D-130); shift then finds nothing to take. */
static void shift_of_a_length_its_buffer_lacks_throws(void) {
  pts_array *array = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], NULL, 0);
  array->length = 1;
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    double out;
    pts_array_shift(array, &out);
    CHECK(false);
  } else {
    CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_RangeError));
  }
  CHECK(pts_array_length_of(array) == 1);
}

/* A [number, string] tuple (D-053): a shape whose fields, named 0 and 1, are its elements. */
typedef struct pair {
  const pts_type *type;
  double first;
  pts_string *second;
} pair;

static const pts_field pair_fields[] = {
    {"0", offsetof(pair, first), PTS_FIELD_NUMBER},
    {"1", offsetof(pair, second), PTS_FIELD_STRING},
};

static const pts_type pair_type = {
    .kind = PTS_KIND_TUPLE,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pair, second)),
    .name = "Array",
    .size = sizeof(pair),
    .field_count = 2,
    .fields = pair_fields,
};

static pts_value new_pair(double first, const char *second) {
  pair *p = pts_object_new(&pair_type);
  p->first = first;
  p->second = pts_string_from_cstr(second);
  return pts_value_from_object(p);
}

static pts_array *values_of(pts_value *items, size_t count) { return pts_array_of(&pts_array_types[PTS_FIELD_VALUE], items, count); }

/* The elements of a number[] as doubles, or of an any[] as numbers (NaN for one that isn't), in expected's length. */
static bool holds_numbers(const pts_array *array, const double *expected, size_t count) {
  if (pts_array_length_of(array) != count) {
    return false;
  }
  for (size_t i = 0; i < count; i++) {
    pts_value element = pts_array_element(array, (double)i);
    if (!pts_value_is_number(element) || pts_value_as_number(element) != expected[i]) {
      return false;
    }
  }
  return true;
}

/* [1, [2, <hole>, [0]], <hole>, [3, "c"]] as an any[]: arrays and a tuple in dynamic slots, holes at two levels. */
static pts_array *nested_with_holes(void) {
  pts_value inner[] = {pts_value_from_number(2), PTS_VALUE_ABSENT, pts_value_from_object(numbers(1))};
  pts_value outer[] = {pts_value_from_number(1), pts_value_from_object(values_of(inner, 3)), PTS_VALUE_ABSENT, new_pair(3, "c")};
  return values_of(outer, 4);
}

static void flat_spreads_arrays_and_tuples_and_skips_holes(void) {
  pts_array *source = nested_with_holes();
  pts_array *one = pts_array_flat(source, 1, &pts_array_types[PTS_FIELD_VALUE]);
  CHECK(pts_array_length_of(one) == 5);
  CHECK(pts_value_as_number(pts_array_element(one, 0)) == 1 && pts_value_as_number(pts_array_element(one, 1)) == 2);
  pts_value nested = pts_array_element(one, 2);
  CHECK(pts_value_is_object(nested) && pts_value_as_object(nested)->type->kind == PTS_KIND_ARRAY);
  CHECK(pts_value_as_number(pts_array_element(one, 3)) == 3);
  CHECK(pts_string_equals(pts_value_as_string(pts_array_element(one, 4)), pts_string_from_cstr("c")));
  pts_array *all = pts_array_flat(source, __builtin_inf(), &pts_array_types[PTS_FIELD_VALUE]);
  CHECK(pts_array_length_of(all) == 5 && pts_value_as_number(pts_array_element(all, 2)) == 0);
}

static void flat_of_depth_zero_or_less_copies_without_holes(void) {
  pts_array *source = nested_with_holes();
  for (double depth = 0; depth >= -1; depth--) {
    pts_array *copy = pts_array_flat(source, depth, &pts_array_types[PTS_FIELD_VALUE]);
    CHECK(pts_array_length_of(copy) == 3);
    CHECK(pts_array_element(copy, 1) == pts_array_element(source, 1) && pts_array_element(copy, 2) == pts_array_element(source, 3));
  }
}

/* (number | number[])[] flattens to a number[]: each leaf is stored as the result's element kind holds it. */
static void flat_stores_leaves_in_the_results_element_kind(void) {
  pts_value items[] = {pts_value_from_number(1), pts_value_from_object(numbers(2)), pts_value_from_number(5)};
  pts_array *flat = pts_array_flat(values_of(items, 3), 1, &pts_array_types[PTS_FIELD_NUMBER]);
  CHECK(flat->type == &pts_array_types[PTS_FIELD_NUMBER]);
  double expected[] = {1, 0, 1, 5};
  CHECK(holds_numbers(flat, expected, 4));
}

/* A leaf the result's element kind has no representation for is a TypeError, never a reinterpreted word. */
static void a_leaf_the_result_cannot_hold_throws(void) {
  pts_value items[] = {pts_value_from_number(1), pts_value_from_string(pts_string_from_cstr("s"))};
  pts_array *source = values_of(items, 2);
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_array_flat(source, 1, &pts_array_types[PTS_FIELD_NUMBER]);
    CHECK(false);
  } else {
    CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError));
  }
}

/* flatMap's step for one callback result: an array's elements (holes skipped) or a tuple's, one level down, else the value. */
static void push_flat_spreads_one_level(void) {
  pts_array *result = values_of(NULL, 0);
  pts_array_push_flat(result, pts_value_from_number(7));
  pts_value nested[] = {pts_value_from_object(numbers(1)), PTS_VALUE_ABSENT, pts_value_from_number(8)};
  pts_array_push_flat(result, pts_value_from_object(values_of(nested, 3)));
  pts_array_push_flat(result, new_pair(9, "i"));
  CHECK(pts_array_length_of(result) == 5);
  CHECK(pts_array_element(result, 1) == nested[0]);
  CHECK(pts_value_as_number(pts_array_element(result, 2)) == 8 && pts_value_as_number(pts_array_element(result, 3)) == 9);
  pts_array *doubles = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], NULL, 0);
  pts_array_push_flat(doubles, pts_value_from_object(numbers(3)));
  pts_array_push_flat(doubles, pts_value_from_number(-1));
  double expected[] = {0, 1, 2, -1};
  CHECK(holds_numbers(doubles, expected, 4));
}

/* An array that holds itself, flattened at depth Infinity, throws JS's RangeError rather than overflow the C stack. */
static void flat_of_a_cycle_throws_range_error(void) {
  pts_array *cycle = values_of(NULL, 0);
  pts_value self = pts_value_from_object(cycle);
  pts_array_push(cycle, &self, 1);
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_array_flat(cycle, __builtin_inf(), &pts_array_types[PTS_FIELD_VALUE]);
    CHECK(false);
  } else {
    CHECK(pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_RangeError));
  }
}

static void has_tells_holes_and_missing_elements(void) {
  pts_array *array = strings_with_a_hole();
  CHECK(pts_array_has(array, 0) && pts_array_has(array, 2));
  CHECK(!pts_array_has(array, 1) && !pts_array_has(array, 3) && !pts_array_has(array, -1));
}

/* A match's groups that took no part are undefined elements (D-133), not holes: flat keeps them, a string[] result becoming one whose holes are undefined. */
static void flat_keeps_the_undefined_groups_of_a_match(void) {
  pts_string *items[] = {pts_string_from_cstr("b"), NULL, pts_string_from_cstr("b")};
  pts_array *groups = pts_array_of(&pts_type_match_copy, items, 3);
  CHECK(pts_array_has(groups, 1) && !pts_array_has(groups, 3));
  pts_array *strings = pts_array_flat(groups, 1, &pts_array_types[PTS_FIELD_STRING]);
  CHECK(pts_array_length_of(strings) == 3 && pts_has_undefined_holes(strings));
  pts_value nested = pts_value_from_object(groups);
  pts_array *values = pts_array_flat(values_of(&nested, 1), 1, &pts_array_types[PTS_FIELD_VALUE]);
  CHECK(pts_array_length_of(values) == 3 && pts_array_element(values, 1) == PTS_VALUE_UNDEFINED);
  pts_array *plain = pts_array_flat(strings_with_a_hole(), 1, &pts_array_types[PTS_FIELD_STRING]);
  CHECK(pts_array_length_of(plain) == 2 && !pts_has_undefined_holes(plain));
  pts_array *both[] = {groups, groups};
  pts_array *twice = pts_array_flat(pts_array_of(&pts_array_types[PTS_FIELD_OBJECT], both, 2), 1, &pts_array_types[PTS_FIELD_STRING]);
  CHECK(pts_array_length_of(twice) == 6 && pts_has_undefined_holes(twice)); /* a second undefined group in the same result */
}

/* A 64-bit integer stored where a number goes is converted, rounding past 2^53 (D-012). */
static void integer_leaves_convert_to_numbers(void) {
  int64_t big = INT64_C(1) << 62;
  pts_array *ints = pts_array_of(&pts_array_types[PTS_FIELD_I64], &big, 1);
  uint64_t huge = UINT64_C(1) << 63;
  pts_array *unsigned_ints = pts_array_of(&pts_array_types[PTS_FIELD_U64], &huge, 1);
  pts_value nested[] = {pts_value_from_number(1.5), pts_value_from_object(ints), pts_value_from_object(unsigned_ints)};
  pts_array *flat = pts_array_flat(values_of(nested, 3), 1, &pts_array_types[PTS_FIELD_NUMBER]);
  double expected[] = {1.5, 4611686018427387904.0, 9223372036854775808.0};
  CHECK(holds_numbers(flat, expected, 3));
}

/* number[][]: arrays of number arrays, which the program describes with their element type (D-072), as it does flat's and flatMap's results (D-180). */
static const pts_check number_check = {.kind = PTS_CHECK_NUMBER, .slot = PTS_FIELD_NUMBER, .name = "number"};
static const pts_check numbers_check = {
    .kind = PTS_CHECK_ARRAY, .slot = PTS_FIELD_OBJECT, .name = "number[]", .element = &number_check, .type = &pts_array_types[PTS_FIELD_NUMBER]};
static const pts_type number_arrays_type = {
    .kind = PTS_KIND_ARRAY,
    .gc_descr = PTS_GC_POINTER_WORDS(PTS_GC_WORD(pts_array, buffer)),
    .name = "Array",
    .size = sizeof(pts_array),
    .element = PTS_FIELD_OBJECT,
    .check = &numbers_check,
};

/* Whether flattening value one level onto result throws a TypeError. */
static bool push_flat_throws_type_error(pts_array *result, pts_value value) {
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_array_push_flat(result, value);
    pts_try_leave(&handler);
    return false;
  }
  return pts_value_instance_of(pts_caught(&handler), &pts_type_builtin_TypeError);
}

/* An element stored in a result of object type is of the result's element type: leaves of a source of the same type are stored as they are, others checked. */
static void leaves_are_checked_against_the_results_element_type(void) {
  pts_array *row = numbers(2);
  pts_array *rows = pts_array_of(&number_arrays_type, &row, 1);
  pts_array *result = pts_array_of(&number_arrays_type, NULL, 0);
  pts_array_push_flat(result, pts_value_from_object(rows));
  CHECK(pts_array_length_of(result) == 1 && pts_array_element(result, 0) == pts_value_from_object(row));
  pts_value loose = pts_value_from_object(row);
  pts_array_push_flat(result, pts_value_from_object(values_of(&loose, 1)));
  CHECK(pts_array_length_of(result) == 2 && pts_array_element(result, 1) == pts_value_from_object(row));
  CHECK(push_flat_throws_type_error(result, pts_value_from_object(row))); /* its numbers */
  pts_value pair = new_pair(1, "p");
  pts_array *objects = pts_array_of(&pts_array_types[PTS_FIELD_OBJECT], &pair, 1);
  CHECK(push_flat_throws_type_error(result, pts_value_from_object(objects))); /* a [number, string] in a slot of the same kind */
  CHECK(pts_array_length_of(result) == 2);
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  pushing_grows_the_buffer_and_keeps_the_elements();
  indices_out_of_range_have_no_slot();
  both_ends_add_and_remove_in_order();
  elements_of_every_kind_box_as_their_values();
  a_cycle_joins_as_empty();
  splice_clamps_and_inserts();
  sort_keeps_equal_keys_in_order();
  sort_with_a_comparator_is_stable();
  racing_pushes_lose_elements_but_stay_in_bounds();
  loads_report_holes_and_missing_elements();
  a_set_of_an_array_leaves_out_holes();
  shift_of_a_length_its_buffer_lacks_throws();
  flat_spreads_arrays_and_tuples_and_skips_holes();
  flat_of_depth_zero_or_less_copies_without_holes();
  flat_stores_leaves_in_the_results_element_kind();
  a_leaf_the_result_cannot_hold_throws();
  push_flat_spreads_one_level();
  flat_of_a_cycle_throws_range_error();
  has_tells_holes_and_missing_elements();
  flat_keeps_the_undefined_groups_of_a_match();
  integer_leaves_convert_to_numbers();
  leaves_are_checked_against_the_results_element_type();
  return CHECK_DONE();
}
