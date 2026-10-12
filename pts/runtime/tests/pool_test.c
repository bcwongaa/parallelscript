/* The fork-join pool (D-122): join, adaptive splitting, exceptions, nesting, and promise work done by the pool's threads. */
#include "pts.h" /* first: it asks for the POSIX declarations, setenv among them */

#include "check.h"

#include <pthread.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdlib.h>

enum { THREADS = 4 };

/* ---------------------------------------------------------------- join */

typedef struct fib_call {
  int n;
  long result;
} fib_call;

static void fib_join(void *argument, bool migrated) {
  (void)migrated;
  fib_call *call = argument;
  if (call->n < 15) {
    long a = 0, b = 1;
    for (int i = 0; i < call->n; i++) {
      long next = a + b;
      a = b;
      b = next;
    }
    call->result = a;
    return;
  }
  fib_call left = {call->n - 1, 0}, right = {call->n - 2, 0};
  pts_join(fib_join, &left, fib_join, &right);
  call->result = left.result + right.result;
}

static void join_runs_both_closures(void) {
  fib_call call = {32, 0};
  fib_join(&call, false);
  CHECK(call.result == 2178309);
}

/* a waits for b, which only another thread can then run: it steals it and says it migrated. */
typedef struct handoff {
  atomic_bool b_ran;
  bool b_migrated;
  pthread_t b_thread;
} handoff;

static void wait_for_b(void *argument, bool migrated) {
  handoff *h = argument;
  CHECK(!migrated);
  for (long spins = 0; !atomic_load(&h->b_ran) && spins < 100000000; spins++) {
    sched_yield();
  }
}

static void run_b(void *argument, bool migrated) {
  handoff *h = argument;
  h->b_migrated = migrated;
  h->b_thread = pthread_self();
  atomic_store(&h->b_ran, true);
}

static void another_thread_takes_the_second_closure_while_the_first_runs(void) {
  handoff h = {0};
  pts_join(wait_for_b, &h, run_b, &h);
  CHECK(atomic_load(&h.b_ran));
  CHECK(h.b_migrated);
  CHECK(!pthread_equal(h.b_thread, pthread_self()));
}

/* ---------------------------------------------------------------- adaptive splitting */

static atomic_int leaves;

static void sum_leaf(const pts_par_op *op, size_t from, size_t to, void *result) {
  (void)op;
  double sum = 0;
  for (size_t i = from; i < to; i++) {
    sum += (double)i;
  }
  *(double *)result = sum;
  atomic_fetch_add(&leaves, 1);
}

static void sum_combine(const pts_par_op *op, void *left, void *right) {
  (void)op;
  *(double *)left += *(double *)right;
}

static void an_operation_covers_every_index_once_in_pieces(void) {
  static const pts_par_op sum = {.leaf = sum_leaf, .combine = sum_combine, .result_size = sizeof(double), .min_length = 1000};
  double result;
  pts_par_run(&sum, 10000000, &result);
  CHECK(result == 10000000.0 * 9999999.0 / 2);
  CHECK(atomic_load(&leaves) >= THREADS);
  CHECK(atomic_load(&leaves) <= 10000000 / 1000);
  pts_par_run(&sum, 0, &result);
  CHECK(result == 0);
  pts_par_run(&sum, 1, &result);
  CHECK(result == 0);
}

/* Pieces of an outer operation that run inner operations: the threads nest joins in each other's work. */
static void nested_leaf(const pts_par_op *op, size_t from, size_t to, void *result) {
  (void)op;
  static const pts_par_op sum = {.leaf = sum_leaf, .combine = sum_combine, .result_size = sizeof(double), .min_length = 100};
  double total = 0;
  for (size_t i = from; i < to; i++) {
    double inner;
    pts_par_run(&sum, 10000, &inner);
    total += inner;
  }
  *(double *)result = total;
}

static void operations_nest(void) {
  static const pts_par_op outer = {.leaf = nested_leaf, .combine = sum_combine, .result_size = sizeof(double), .min_length = 1};
  double result;
  pts_par_run(&outer, 64, &result);
  CHECK(result == 64 * (10000.0 * 9999.0 / 2));
}

/* ---------------------------------------------------------------- exceptions */

static void throwing_leaf(const pts_par_op *op, size_t from, size_t to, void *result) {
  (void)op, (void)result;
  for (size_t i = from; i < to; i++) {
    if (i == 777 || i == 900000) {
      pts_throw_new(&pts_type_builtin_RangeError, "index %zu", i);
    }
  }
}

static void a_throw_in_a_piece_is_rethrown_after_the_others_are_done(void) {
  static const pts_par_op op = {.leaf = throwing_leaf, .min_length = 100};
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    pts_par_run(&op, 1000000, NULL);
    pts_try_leave(&handler);
    CHECK(!"the operation should have thrown");
    return;
  }
  pts_value caught = pts_caught(&handler);
  pts_string *message = ((pts_error *)pts_value_as_object(caught))->message;
  size_t length;
  const char *text = pts_string_to_utf8(message, &length);
  CHECK(length == 9 && memcmp(text, "index 777", 9) == 0); /* the leftmost piece's, as a's comes before b's */
}

/* ---------------------------------------------------------------- promise work on the pool's threads */

static pthread_t main_thread;
static atomic_int ran_on_main;

static pts_value count_callback(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  atomic_fetch_add(&ran_on_main, pthread_equal(pthread_self(), main_thread));
  return PTS_VALUE_UNDEFINED;
}

static const pts_type callback_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .call = count_callback};

static void queueing_leaf(const pts_par_op *op, size_t from, size_t to, void *result) {
  (void)op;
  for (size_t i = from; i < to; i++) {
    pts_queue_microtask(pts_value_from_object(pts_object_new(&callback_type)));
  }
  *(double *)result = (double)(to - from);
}

static void microtasks_queued_by_pieces_run_on_the_operations_loop(void) {
  static const pts_par_op op = {.leaf = queueing_leaf, .combine = sum_combine, .result_size = sizeof(double), .min_length = 1};
  double queued;
  pts_par_run(&op, 500, &queued);
  CHECK(queued == 500);
  pts_run_microtasks();
  pts_run_loop(); /* the jobs other threads queued are messages to this loop */
  CHECK(atomic_load(&ran_on_main) == 500);
}

int main(int argc, char **argv) {
  setenv("PTS_THREADS", "4", 1);
  pts_runtime_init(argc, argv);
  pts_loop_init();
  main_thread = pthread_self();
  CHECK(pts_pool_threads() == THREADS);
  join_runs_both_closures();
  another_thread_takes_the_second_closure_while_the_first_runs();
  an_operation_covers_every_index_once_in_pieces();
  operations_nest();
  a_throw_in_a_piece_is_rethrown_after_the_others_are_done();
  microtasks_queued_by_pieces_run_on_the_operations_loop();
  return CHECK_DONE();
}
