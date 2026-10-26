#include "pts.h"

const pts_type pts_int64_type = {
    .kind = PTS_KIND_INT64, .gc_descr = PTS_GC_NO_POINTERS, .name = "int", .size = sizeof(pts_boxed_int64)};
const pts_type pts_uint64_type = {
    .kind = PTS_KIND_UINT64, .gc_descr = PTS_GC_NO_POINTERS, .name = "u64", .size = sizeof(pts_boxed_uint64)};

/* The largest magnitude up to which every integer is exactly a double. */
#define EXACT_DOUBLE_LIMIT (INT64_C(1) << 53)

void pts_throw_division_by_zero(void) { pts_throw_new(&pts_type_builtin_RangeError, "Division by zero"); }

void pts_throw_negative_exponent(void) { pts_throw_new(&pts_type_builtin_RangeError, "Exponent must be non-negative"); }

pts_value pts_value_from_i64(int64_t value) {
  if (value >= -EXACT_DOUBLE_LIMIT && value <= EXACT_DOUBLE_LIMIT) {
    return pts_value_from_number((double)value);
  }
  pts_boxed_int64 *box = GC_MALLOC_ATOMIC(sizeof *box);
  box->type = &pts_int64_type;
  box->value = value;
  pts_publish();
  return pts_value_from_object(box);
}

pts_value pts_value_from_u64(uint64_t value) {
  if (value <= (uint64_t)EXACT_DOUBLE_LIMIT) {
    return pts_value_from_number((double)value);
  }
  pts_boxed_uint64 *box = GC_MALLOC_ATOMIC(sizeof *box);
  box->type = &pts_uint64_type;
  box->value = value;
  pts_publish();
  return pts_value_from_object(box);
}

int64_t pts_value_as_i64(pts_value value) {
  if (pts_value_is_number(value)) {
    return (int64_t)pts_value_as_number(value);
  }
  return ((const pts_boxed_int64 *)pts_value_as_object(value))->value;
}

uint64_t pts_value_as_u64(pts_value value) {
  if (pts_value_is_number(value)) {
    return (uint64_t)pts_value_as_number(value);
  }
  return ((const pts_boxed_uint64 *)pts_value_as_object(value))->value;
}
