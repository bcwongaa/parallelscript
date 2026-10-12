/*
 * Typed arrays shared by threads (D-143): element reads and writes, fills, reversals,
 * sorts and bulk copies racing on one array. Every element read is a whole value some
 * thread wrote, and under ThreadSanitizer every access is atomic (no data race reported).
 */
#include "pts.h" /* first: it asks for the POSIX declarations */

#include "check.h"

#include <math.h>
#include <pthread.h>
#include <stdatomic.h>

enum { THREADS = 4, ROUNDS = 4000, LENGTH = 256 };

/* The values written: whole doubles whose halves, mixed, are none of them. */
static const double VALUES[] = {0, 1.5, -2.25, 1e300, -3.0e-300};
enum { VALUE_COUNT = sizeof VALUES / sizeof VALUES[0] };

static pts_typed_array *shared;
static atomic_int torn;

static bool is_written(double value) {
  for (size_t i = 0; i < VALUE_COUNT; i++) {
    if (value == VALUES[i]) {
      return true;
    }
  }
  return false;
}

static void check_elements(const pts_typed_array *array) {
  for (size_t i = 0; i < array->length; i++) {
    if (!is_written(pts_typed_array_as_number(array, i))) {
      atomic_fetch_add(&torn, 1);
    }
  }
}

static void *race(void *argument) {
  size_t thread = (size_t)argument;
  for (size_t round = 0; round < ROUNDS; round++) {
    size_t at = (round * 31 + thread * 7) % LENGTH;
    pts_Float64Array_set(shared, (double)at, VALUES[(round + thread) % VALUE_COUNT]);
    if (!is_written(pts_Float64Array_get(shared, (double)((at * 13) % LENGTH)))) {
      atomic_fetch_add(&torn, 1);
    }
    switch (round % 8) {
    case 0:
      pts_typed_array_copy_within(shared, (double)(at % 64), (double)(at % 32 + 64), (double)(at % 32 + 192));
      break;
    case 1:
      pts_typed_array_set_typed(shared, pts_typed_array_subarray(shared, (double)(at % 16), (double)(at % 16 + 100)), (double)(at % 128));
      break;
    case 2:
      check_elements(pts_typed_array_slice(shared, (double)(at % 64), INFINITY));
      break;
    case 3:
      pts_typed_array_fill(shared, VALUES[round % VALUE_COUNT], (double)at, (double)(at + 16));
      break;
    case 4:
      pts_typed_array_reverse(pts_typed_array_subarray(shared, (double)(at % 128), (double)(at % 128 + 64)));
      break;
    case 5:
      pts_typed_array_sort(pts_typed_array_subarray(shared, (double)(at % 192), (double)(at % 192 + 32)));
      break;
    case 6:
      check_elements(pts_typed_array_on_buffer(&pts_type_builtin_Float64Array, pts_arraybuffer_slice(shared->buffer, (double)(at % 32 * 8), INFINITY), 0, 0, false));
      break;
    default:
      check_elements(pts_typed_array_from_typed(&pts_type_builtin_Float64Array, shared));
    }
  }
  return NULL;
}

static void racing_threads_see_only_whole_elements(void) {
  shared = pts_typed_array_new(&pts_type_builtin_Float64Array, LENGTH);
  pthread_t threads[THREADS];
  for (size_t i = 0; i < THREADS; i++) {
    pthread_create(&threads[i], NULL, race, (void *)i);
  }
  for (size_t i = 0; i < THREADS; i++) {
    pthread_join(threads[i], NULL);
  }
  check_elements(shared);
  CHECK(atomic_load(&torn) == 0);
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  racing_threads_see_only_whole_elements();
  return CHECK_DONE();
}
