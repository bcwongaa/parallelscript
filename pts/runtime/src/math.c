/*
 * The Math functions beyond fdlibm's (ieee754.c) and the inline ones in pts.h (D-084):
 * max, min and hypot of an array of arguments, and random.
 */
#include "pts.h"

#include <math.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
#if defined(__APPLE__)
#include <sys/random.h>
#endif

static double argument_at(const pts_array *values, size_t i) { return *(double *)pts_array_slot(values, (double)i, sizeof(double)); }

double pts_math_max_of(pts_array *values) {
  double result = -INFINITY;
  for (size_t i = 0, n = pts_array_length_of(values); i < n; i++) {
    result = pts_math_max(result, argument_at(values, i));
  }
  return result;
}

double pts_math_min_of(pts_array *values) {
  double result = INFINITY;
  for (size_t i = 0, n = pts_array_length_of(values); i < n; i++) {
    result = pts_math_min(result, argument_at(values, i));
  }
  return result;
}

/* V8's Math.hypot: Infinity if any argument is infinite, else NaN if one is NaN; else Kahan summation of the squares scaled by the largest. */
double pts_math_hypot(pts_array *values) {
  size_t n = pts_array_length_of(values);
  double max = 0;
  bool one_nan = false;
  for (size_t i = 0; i < n; i++) {
    double magnitude = fabs(argument_at(values, i));
    if (magnitude != magnitude) {
      one_nan = true;
    } else if (magnitude > max) {
      max = magnitude;
    }
  }
  if (max == INFINITY) {
    return INFINITY;
  }
  if (one_nan) {
    return NAN;
  }
  if (max == 0) {
    return 0;
  }
  double sum = 0, compensation = 0;
  for (size_t i = 0; i < n; i++) {
    double scaled = fabs(argument_at(values, i)) / max;
    double summand = scaled * scaled - compensation;
    double preliminary = sum + summand;
    compensation = (preliminary - sum) - summand;
    sum = preliminary;
  }
  return sqrt(sum) * max;
}

/* xorshift128+, as V8's Math.random, with a state per thread seeded from the system's entropy. */
static _Thread_local uint64_t random_state[2];

static void seed_random(void) {
  if (getentropy(random_state, sizeof random_state) != 0 || (random_state[0] | random_state[1]) == 0) {
    struct timespec now;
    clock_gettime(CLOCK_REALTIME, &now);
    random_state[0] = (uint64_t)now.tv_nsec * 0x9E3779B97F4A7C15u ^ (uint64_t)(uintptr_t)&random_state;
    random_state[1] = (uint64_t)now.tv_sec * 0xBF58476D1CE4E5B9u | 1;
  }
}

double pts_math_random(void) {
  if ((random_state[0] | random_state[1]) == 0) {
    seed_random();
  }
  uint64_t s1 = random_state[0], s0 = random_state[1];
  random_state[0] = s0;
  s1 ^= s1 << 23;
  s1 ^= s1 >> 17;
  s1 ^= s0;
  s1 ^= s0 >> 26;
  random_state[1] = s1;
  uint64_t bits = (random_state[0] >> 12) | UINT64_C(0x3FF0000000000000); /* in [1, 2) */
  double number;
  memcpy(&number, &bits, sizeof number);
  return number - 1;
}
