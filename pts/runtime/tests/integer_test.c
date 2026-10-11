#include "pts.h"

#include <math.h>

#include "check.h"

static void arithmetic_wraps(void) {
  CHECK(pts_i32_add(INT32_MAX, 1) == INT32_MIN);
  CHECK(pts_i32_sub(INT32_MIN, 1) == INT32_MAX);
  CHECK(pts_i64_add(INT64_MAX, 1) == INT64_MIN);
  CHECK(pts_i64_mul(INT64_MAX, 2) == -2);
  CHECK(pts_i64_neg(INT64_MIN) == INT64_MIN);
  CHECK(pts_u8_add(200, 100) == 44);
  CHECK(pts_u8_sub(0, 1) == 255);
  CHECK(pts_u16_mul(65535, 65535) == 1); /* promoted to int this would overflow */
  CHECK(pts_i8_mul(-128, -1) == -128);
  CHECK(pts_u64_add(UINT64_MAX, 2) == 1);
  CHECK(pts_u32_neg(1) == UINT32_MAX);
}

static void division_truncates(void) {
  CHECK(pts_i32_div(7, 2) == 3);
  CHECK(pts_i32_div(-7, 2) == -3);
  CHECK(pts_i32_rem(-7, 2) == -1);
  CHECK(pts_i32_rem(7, -2) == 1);
  CHECK(pts_i64_div(INT64_MIN, -1) == INT64_MIN);
  CHECK(pts_i64_rem(INT64_MIN, -1) == 0);
  CHECK(pts_i8_div(-128, -1) == -128);
  CHECK(pts_u8_div(255, 2) == 127);
  CHECK(pts_u64_rem(UINT64_MAX, 10) == 5);
}

static void power_wraps(void) {
  CHECK(pts_i64_pow(2, 10) == 1024);
  CHECK(pts_i64_pow(2, 63) == INT64_MIN);
  CHECK(pts_i64_pow(-3, 3) == -27);
  CHECK(pts_i32_pow(7, 0) == 1);
  CHECK(pts_u8_pow(2, 8) == 0);
  CHECK(pts_u64_pow(3, 41) == UINT64_C(18026252303461234787)); /* 3^41 mod 2^64 */
}

static void float_to_integer_saturates_like_rust(void) {
  CHECK(pts_number_to_i32(3.9) == 3);
  CHECK(pts_number_to_i32(-3.9) == -3);
  CHECK(pts_number_to_i32(NAN) == 0);
  CHECK(pts_number_to_i32(1e10) == INT32_MAX);
  CHECK(pts_number_to_i32(-INFINITY) == INT32_MIN);
  CHECK(pts_number_to_u8(300.5) == 255);
  CHECK(pts_number_to_u8(-5) == 0);
  CHECK(pts_number_to_u8(255.9) == 255);
  CHECK(pts_number_to_i8(-128.9) == -128);
  CHECK(pts_number_to_i64(9.3e18) == INT64_MAX);
  CHECK(pts_number_to_i64(-9.3e18) == INT64_MIN);
  CHECK(pts_number_to_i64(9007199254740993.0) == INT64_C(9007199254740992));
  CHECK(pts_number_to_u64(-1) == 0);
  CHECK(pts_number_to_u64(1.9e19) == UINT64_MAX);
  CHECK(pts_number_to_u64(NAN) == 0);
}

static void sixty_four_bit_values_box_only_beyond_two_to_the_53(void) {
  CHECK(pts_value_is_number(pts_value_from_i64(INT64_C(9007199254740992))));
  CHECK(pts_value_is_number(pts_value_from_i64(-INT64_C(9007199254740992))));
  pts_value big = pts_value_from_i64(INT64_C(9007199254740993));
  CHECK(pts_value_is_object(big));
  CHECK(pts_value_as_object(big)->type->kind == PTS_KIND_INT64);
  CHECK(((pts_boxed_int64 *)pts_value_as_object(big))->value == INT64_C(9007199254740993));
  pts_value huge = pts_value_from_u64(UINT64_MAX);
  CHECK(pts_value_is_object(huge));
  CHECK(((pts_boxed_uint64 *)pts_value_as_object(huge))->value == UINT64_MAX);
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  arithmetic_wraps();
  division_truncates();
  power_wraps();
  float_to_integer_saturates_like_rust();
  sixty_four_bit_values_box_only_beyond_two_to_the_53();
  return pts_runtime_exit(CHECK_DONE());
}
