#include "pts.h"

const pts_type pts_int64_type = {PTS_KIND_INT64, "int"};
const pts_type pts_uint64_type = {PTS_KIND_UINT64, "u64"};

/* The largest magnitude up to which every integer is exactly a double. */
#define EXACT_DOUBLE_LIMIT (INT64_C(1) << 53)

void pts_throw_division_by_zero(void) { pts_panic("RangeError", "Division by zero"); }

void pts_throw_negative_exponent(void) { pts_panic("RangeError", "Exponent must be non-negative"); }

pts_value pts_value_from_i64(int64_t value) {
  if (value >= -EXACT_DOUBLE_LIMIT && value <= EXACT_DOUBLE_LIMIT) {
    return pts_value_from_number((double)value);
  }
  pts_boxed_int64 *box = GC_MALLOC_ATOMIC(sizeof *box);
  box->type = &pts_int64_type;
  box->value = value;
  return pts_value_from_object(box);
}

pts_value pts_value_from_u64(uint64_t value) {
  if (value <= (uint64_t)EXACT_DOUBLE_LIMIT) {
    return pts_value_from_number((double)value);
  }
  pts_boxed_uint64 *box = GC_MALLOC_ATOMIC(sizeof *box);
  box->type = &pts_uint64_type;
  box->value = value;
  return pts_value_from_object(box);
}
