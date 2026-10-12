#include "pts.h"

#include <math.h>

#include "check.h"

/* Expected values are what Node gives. */

static const char *joined(const pts_typed_array *array) { return pts_typed_array_join(array, NULL)->bytes; }

static pts_typed_array *of(const pts_type *type, const double *numbers, size_t count) {
  pts_typed_array *array = pts_typed_array_new(type, (double)count);
  for (size_t i = 0; i < count; i++) {
    pts_typed_array_set(array, (double)i, numbers[i]);
  }
  return array;
}

static void writes_convert_as_js_does(void) {
  double values[] = {1, 300, -1, 1.7, NAN, -129.5, 65536 + 7, 4294967296.0 * 3 + 5, INFINITY};
  CHECK_STR(joined(of(&pts_type_builtin_Uint8Array, values, 9)), "1,44,255,1,0,127,7,5,0");
  CHECK_STR(joined(of(&pts_type_builtin_Int8Array, values, 9)), "1,44,-1,1,0,127,7,5,0");
  CHECK_STR(joined(of(&pts_type_builtin_Int16Array, values, 9)), "1,300,-1,1,0,-129,7,5,0");
  CHECK_STR(joined(of(&pts_type_builtin_Uint32Array, values, 9)), "1,300,4294967295,1,0,4294967167,65543,5,0");
  double clamped[] = {300, -5, 1.5, 2.5, 0.5, NAN, 254.6};
  CHECK_STR(joined(of(&pts_type_builtin_Uint8ClampedArray, clamped, 7)), "255,0,2,2,0,0,255");
  double floats[] = {0.1, -0.0, 1e40};
  CHECK_STR(joined(of(&pts_type_builtin_Float32Array, floats, 3)), "0.10000000149011612,0,Infinity");
}

static void indices_out_of_range_read_undefined_and_ignore_writes(void) {
  pts_typed_array *array = pts_typed_array_new(&pts_type_builtin_Float64Array, 2);
  CHECK(pts_Float64Array_set(array, 2, 5) == 5);
  CHECK(pts_Float64Array_set(array, 0.5, 5) == 5);
  CHECK(pts_Float64Array_set(array, -1, 5) == 5);
  CHECK_STR(joined(array), "0,0");
  CHECK(pts_typed_array_element(array, 2) == PTS_VALUE_UNDEFINED);
  CHECK(pts_typed_array_element(array, 1) == pts_value_from_number(0));
  CHECK(pts_value_as_number(pts_typed_array_at(array, -1)) == 0);
  CHECK(pts_typed_array_at(array, -3) == PTS_VALUE_UNDEFINED);
}

static void subarrays_share_the_buffer(void) {
  double values[] = {1, 2, 3, 4, 5};
  pts_typed_array *array = of(&pts_type_builtin_Uint16Array, values, 5);
  pts_typed_array *middle = pts_typed_array_subarray(array, 1, -1);
  CHECK(middle->length == 3 && middle->byte_offset == 2 && middle->buffer == array->buffer);
  pts_Uint16Array_set(middle, 0, 20);
  CHECK_STR(joined(array), "1,20,3,4,5");
  pts_typed_array *copy = pts_typed_array_slice(array, -2, INFINITY);
  pts_Uint16Array_set(copy, 0, 40);
  CHECK_STR(joined(array), "1,20,3,4,5");
  CHECK_STR(joined(copy), "40,5");
  CHECK(pts_typed_array_subarray(array, 4, 2)->length == 0);
}

static void views_of_a_buffer_see_its_bytes(void) {
  pts_typed_array *doubles = pts_typed_array_new(&pts_type_builtin_Float64Array, 1);
  pts_Float64Array_set(doubles, 0, 1);
  pts_typed_array *bytes = pts_typed_array_on_buffer(&pts_type_builtin_Uint8Array, doubles->buffer, 0, 0, false);
  CHECK_STR(joined(bytes), "0,0,0,0,0,0,240,63");
  pts_typed_array *tail = pts_typed_array_on_buffer(&pts_type_builtin_Uint16Array, doubles->buffer, 6, 1, true);
  CHECK(pts_Uint16Array_get(tail, 0) == 0x3ff0);
}

static void sort_is_numeric_with_negative_zero_first_and_nan_last(void) {
  double values[] = {3, NAN, 0, -0.0, -INFINITY, 1, -2};
  pts_typed_array *array = pts_typed_array_sort(of(&pts_type_builtin_Float64Array, values, 7));
  CHECK_STR(joined(array), "-Infinity,-2,0,0,1,3,NaN");
  CHECK(signbit(pts_Float64Array_get(array, 2)) && !signbit(pts_Float64Array_get(array, 3)));
  double integers[] = {10, 9, 1, 100};
  CHECK_STR(joined(pts_typed_array_sort(of(&pts_type_builtin_Int32Array, integers, 4))), "1,9,10,100");
}

static void set_copies_overlapping_views_as_if_first_copied(void) {
  double values[] = {1, 2, 3, 4};
  pts_typed_array *bytes = of(&pts_type_builtin_Uint8Array, values, 4);
  pts_typed_array *wide = pts_typed_array_on_buffer(&pts_type_builtin_Uint16Array, bytes->buffer, 0, 1, true);
  pts_typed_array_set_typed(bytes, wide, 3); /* 0x0201 = 513, written as a byte: 1 */
  CHECK_STR(joined(bytes), "1,2,3,1");
  pts_typed_array_set_typed(bytes, pts_typed_array_subarray(bytes, 0, 3), 1);
  CHECK_STR(joined(bytes), "1,1,2,3");
}

static void searches_follow_strict_equality_and_same_value_zero(void) {
  double values[] = {1, NAN, 3, 1};
  pts_typed_array *array = of(&pts_type_builtin_Float32Array, values, 4);
  CHECK(pts_typed_array_index_of(array, NAN, 0) == -1);
  CHECK(pts_typed_array_includes(array, NAN, 0));
  CHECK(pts_typed_array_index_of(array, 1, 1) == 3);
  CHECK(pts_typed_array_last_index_of(array, 1, INFINITY) == 3);
  CHECK(pts_typed_array_last_index_of(array, 1, -2) == 0);
}

static void copy_within_moves_overlapping_ranges(void) {
  double values[] = {1, 2, 3, 4, 5};
  CHECK_STR(joined(pts_typed_array_copy_within(of(&pts_type_builtin_Int8Array, values, 5), 0, 3, INFINITY)), "4,5,3,4,5");
  CHECK_STR(joined(pts_typed_array_copy_within(of(&pts_type_builtin_Int8Array, values, 5), 1, 0, 3)), "1,1,2,3,5");
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  writes_convert_as_js_does();
  indices_out_of_range_read_undefined_and_ignore_writes();
  subarrays_share_the_buffer();
  views_of_a_buffer_see_its_bytes();
  sort_is_numeric_with_negative_zero_first_and_nan_last();
  set_copies_overlapping_views_as_if_first_copied();
  searches_follow_strict_equality_and_same_value_zero();
  copy_within_moves_overlapping_ranges();
  return CHECK_DONE();
}
