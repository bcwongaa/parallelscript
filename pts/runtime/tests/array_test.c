/* Arrays: growth, the operations at both ends, boxing, and threads racing on one array. */
#include "pts.h"

#include "check.h"

#include <pthread.h>

enum { THREADS = 4, PUSHES = 20000 };

static pts_array *numbers(size_t count) {
  pts_array *array = pts_array_of(PTS_FIELD_NUMBER, NULL, 0);
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
  pts_array *strings = pts_array_of(PTS_FIELD_STRING, &word, 1);
  CHECK(pts_array_element(strings, 0) == pts_value_from_string(word));
  bool flags[] = {true, false};
  pts_array *bools = pts_array_of(PTS_FIELD_BOOL, flags, 2);
  CHECK(pts_array_element(bools, 0) == PTS_VALUE_TRUE && pts_array_element(bools, 1) == PTS_VALUE_FALSE);
  uint8_t bytes[] = {255, 7};
  pts_array *small = pts_array_of(PTS_FIELD_U8, bytes, 2);
  CHECK(pts_value_as_number(pts_array_element(small, 0)) == 255);
  uint8_t more = 9;
  pts_array_push(small, &more, 1);
  CHECK(PTS_ARRAY_GET(uint8_t, small, 2) == 9 && pts_array_length(small) == 3);
}

static void a_cycle_joins_as_empty(void) {
  pts_array *outer = pts_array_of(PTS_FIELD_OBJECT, NULL, 0);
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
  pts_array *array = pts_array_of(PTS_FIELD_STRING, words, 4);
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
  shared = pts_array_of(PTS_FIELD_NUMBER, NULL, 0);
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

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  pushing_grows_the_buffer_and_keeps_the_elements();
  indices_out_of_range_have_no_slot();
  both_ends_add_and_remove_in_order();
  elements_of_every_kind_box_as_their_values();
  a_cycle_joins_as_empty();
  splice_clamps_and_inserts();
  sort_keeps_equal_keys_in_order();
  racing_pushes_lose_elements_but_stay_in_bounds();
  return CHECK_DONE();
}
