/*
 * The loops and tasks (D-121): tasks run on every loop, promises cross loops, calls run on
 * the loops they are posted to (D-137), a timer cleared from another loop never fires
 * (D-139), and the program ends once every loop is idle.
 */
#include "pts.h" /* first: it asks for the POSIX declarations, setenv among them */

#include "../src/scheduler_internal.h"
#include "check.h"

#include <stdatomic.h>
#include <stdlib.h>

enum { TASKS = 2000 };

static double fib(double n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }

/* A task, a closure of no parameters returning a value: fib(n), or with nested set, the promise of a task it starts. */
typedef struct fib_task {
  pts_closure closure;
  double n;
  bool nested;
} fib_task;

static pts_value run_fib_task(pts_closure *self);

static const pts_type fib_task_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "task", .size = sizeof(fib_task)};

static pts_value new_fib_task(double n, bool nested) {
  fib_task *task = pts_object_new(&fib_task_type);
  task->closure.code = (pts_function)run_fib_task;
  task->n = n;
  task->nested = nested;
  return pts_value_from_object(task);
}

static pts_value run_fib_task(pts_closure *self) {
  fib_task *task = (fib_task *)self;
  if (task->nested) {
    return pts_parallel(new_fib_task(task->n, false));
  }
  return pts_value_from_number(fib(task->n));
}

/* A handler of then(): records the sum of the numbers in the array it is given. */
static double sum;
static atomic_int handled;

static pts_value record_sum(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self;
  pts_array *values = (pts_array *)pts_value_as_object(argv[0]);
  CHECK(argc == 1 && pts_array_length_of(values) == TASKS);
  for (size_t i = 0; i < pts_array_length_of(values); i++) {
    sum += pts_value_as_number(pts_array_element(values, (double)i));
  }
  atomic_fetch_add(&handled, 1);
  return PTS_VALUE_UNDEFINED;
}

static const pts_type handler_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .length = 1, .call = record_sum};

static void tasks_on_every_loop_settle_their_promises_on_the_starting_loop(void) {
  pts_array *promises = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  double expected = 0;
  for (int i = 0; i < TASKS; i++) {
    double n = 10 + i % 8;
    pts_value promise = pts_parallel(new_fib_task(n, i % 3 == 0));
    pts_array_push(promises, &promise, 1);
    expected += fib(n);
  }
  pts_value all = pts_promise_all(pts_value_from_object(promises), pts_string_from_cstr("promises"));
  pts_closure *handler = pts_object_new(&handler_type);
  pts_promise_then(all, pts_value_from_object(handler), PTS_VALUE_UNDEFINED);
  pts_run_microtasks();
  pts_run_loop();
  CHECK(atomic_load(&handled) == 1);
  CHECK(sum == expected);
}

/* Calls posted to each loop, two per loop: the loop each ran on, and its place among its loop's calls. */
enum { LOOPS = 4, CALLS = 2 * LOOPS };
static atomic_int ran_on[CALLS];
static atomic_int place[CALLS];
static atomic_int calls_per_loop[LOOPS];

static void record_call(void *data) {
  int call = (int)(intptr_t)data;
  atomic_store(&ran_on[call], pts_current_loop);
  atomic_store(&place[call], atomic_fetch_add(&calls_per_loop[pts_current_loop], 1));
}

static void post_two_calls_to_every_loop(void) {
  CHECK(pts_start_loops() == LOOPS);
  for (int call = 0; call < CALLS; call++) {
    pts_post_call((uint16_t)(call / 2), record_call, (void *)(intptr_t)call);
  }
}

/* After the loops ran: each call ran once, on its loop, the first posted first. */
static void calls_ran_on_their_loops_in_order(void) {
  for (int call = 0; call < CALLS; call++) {
    CHECK(atomic_load(&ran_on[call]) == call / 2);
    CHECK(atomic_load(&place[call]) == call % 2);
  }
  for (int loop = 0; loop < LOOPS; loop++) {
    CHECK(atomic_load(&calls_per_loop[loop]) == 2);
  }
}

/* A minute's timer of the main loop, cleared from another loop: it never fires, and the loops end without waiting for it. */
static atomic_int timer_fired;

static pts_value count_firing(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  atomic_fetch_add(&timer_fired, 1);
  return PTS_VALUE_UNDEFINED;
}

static const pts_type firing_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .call = count_firing};

static void clear_timer(void *timer) { pts_timer_clear(pts_value_from_object(timer)); }

static void clear_a_timer_from_another_loop(void) {
  pts_value timer = pts_timer_set(pts_value_from_object(pts_object_new(&firing_type)), pts_value_from_number(60000), NULL, false);
  pts_post_call(1, clear_timer, pts_value_as_object(timer));
}

/* The loops run once per program, so the tests share the one pts_run_loop. */
int main(int argc, char **argv) {
  setenv("PTS_THREADS", "4", 1);
  pts_runtime_init(argc, argv);
  pts_loop_init();
  post_two_calls_to_every_loop();
  clear_a_timer_from_another_loop();
  tasks_on_every_loop_settle_their_promises_on_the_starting_loop();
  calls_ran_on_their_loops_in_order();
  CHECK(atomic_load(&timer_fired) == 0);
  return CHECK_DONE();
}
