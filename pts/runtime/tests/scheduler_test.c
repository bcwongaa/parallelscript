/*
 * The loops and tasks (D-121): tasks run on every loop, promises cross loops, calls run on
 * the loops they are posted to (D-137), a timer cleared from another loop never fires
 * (D-139), unreferenced timers of loops that sleep fire while another loop keeps the program
 * running (C120), and the program ends once every loop is idle, without waiting for them.
 */
#include "pts.h" /* first: it asks for the POSIX declarations, setenv among them */

#include "../src/scheduler_internal.h"
#include "check.h"

#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <uv.h>

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

/*
 * Unreferenced timers of loops that sleep (C120): once the tasks are done and the other
 * loops sleep, loop 1's unreferenced 30 ms timer fires while the main loop's referenced
 * 300 ms timer keeps the program running (before it, and not early), and loop 3's
 * unreferenced 5 ms interval ticks meanwhile; loop 2's unreferenced 10-minute timer neither
 * fires nor keeps the loops running, and neither does the interval. Loop 2 also has RACING
 * unreferenced timers due over 40 ms, which the main loop clears at about 20 ms, while some
 * are firing: each fires at most once (and ThreadSanitizer sees the clears race the wakes).
 */
enum { UNREF_MS = 30, MAIN_MS = 300, LONG_MS = 600000, INTERVAL_MS = 5, POLL_MS = 10, RACING = 200, RACING_SPREAD_MS = 40, CLEAR_MS = 20 };
static atomic_int unref_fired, long_fired, interval_ticks;
static atomic_int unref_fired_by_main = -1, ticks_by_main = -1;
static _Atomic uint64_t phase_started_at, unref_fired_at; /* uv_hrtime's ns */

static pts_value on_unref(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  atomic_store(&unref_fired_at, uv_hrtime());
  atomic_fetch_add(&unref_fired, 1);
  return PTS_VALUE_UNDEFINED;
}

static pts_value on_long(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  atomic_fetch_add(&long_fired, 1);
  return PTS_VALUE_UNDEFINED;
}

static pts_value on_tick(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  atomic_fetch_add(&interval_ticks, 1);
  return PTS_VALUE_UNDEFINED;
}

static pts_value on_main_timer(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  atomic_store(&unref_fired_by_main, atomic_load(&unref_fired));
  atomic_store(&ticks_by_main, atomic_load(&interval_ticks));
  return PTS_VALUE_UNDEFINED;
}

static const pts_type unref_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .call = on_unref};
static const pts_type long_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .call = on_long};
static const pts_type tick_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .call = on_tick};
static const pts_type main_timer_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .call = on_main_timer};

/* An unreferenced timer for a loop to set on itself. */
typedef struct unreferenced_timer {
  const pts_type *callback;
  double ms;
  bool repeat;
} unreferenced_timer;

static unreferenced_timer short_timer = {&unref_type, UNREF_MS, false};
static unreferenced_timer long_timer = {&long_type, LONG_MS, false};
static unreferenced_timer interval = {&tick_type, INTERVAL_MS, true};

static pts_value set_timer(const pts_type *callback, double ms, bool repeat) {
  return pts_timer_set(pts_value_from_object(pts_object_new(callback)), pts_value_from_number(ms), NULL, repeat);
}

static void set_unreferenced(void *data) {
  const unreferenced_timer *timer = data;
  pts_timer_ref(set_timer(timer->callback, timer->ms, timer->repeat), false);
}

/* Loop 2's racing timers, each a closure that knows its index, published to the main loop by racing_ready. */
typedef struct racing_callback {
  pts_closure closure;
  int index;
} racing_callback;

static pts_value racing[RACING];
static atomic_bool racing_ready;
static atomic_int racing_fired[RACING];

static pts_value on_racing(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  atomic_fetch_add(&racing_fired[((racing_callback *)self)->index], 1);
  return PTS_VALUE_UNDEFINED;
}

static const pts_type racing_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(racing_callback), .call = on_racing};

static void set_racing_timers(void *data) {
  (void)data;
  for (int i = 0; i < RACING; i++) {
    racing_callback *callback = pts_object_new(&racing_type);
    callback->index = i;
    racing[i] = pts_timer_ref(pts_timer_set(pts_value_from_object(callback), pts_value_from_number(1 + i % RACING_SPREAD_MS), NULL, false), false);
  }
  atomic_store_explicit(&racing_ready, true, memory_order_release);
}

/* On the main loop at about CLEAR_MS: clears loop 2's racing timers, some of them due by now (or later, if loop 2 hasn't set them yet). */
static pts_value clear_racing_timers(pts_closure *self, uint32_t argc, const pts_value *argv);
static const pts_type clear_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .call = clear_racing_timers};

static pts_value clear_racing_timers(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  if (!atomic_load_explicit(&racing_ready, memory_order_acquire)) {
    set_timer(&clear_type, 1, false);
    return PTS_VALUE_UNDEFINED;
  }
  for (int i = 0; i < RACING; i++) {
    pts_timer_clear(racing[i]);
  }
  return PTS_VALUE_UNDEFINED;
}

/* On the main loop, every POLL_MS until the tasks are done: then the timers. */
static pts_value start_when_the_tasks_are_done(pts_closure *self, uint32_t argc, const pts_value *argv);
static const pts_type poll_type = {.kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(pts_closure), .call = start_when_the_tasks_are_done};

static pts_value start_when_the_tasks_are_done(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)self, (void)argc, (void)argv;
  if (atomic_load(&handled) == 0) {
    set_timer(&poll_type, POLL_MS, false);
    return PTS_VALUE_UNDEFINED;
  }
  atomic_store(&phase_started_at, uv_hrtime());
  pts_post_call(1, set_unreferenced, &short_timer);
  pts_post_call(2, set_unreferenced, &long_timer);
  pts_post_call(3, set_unreferenced, &interval);
  pts_post_call(2, set_racing_timers, NULL);
  set_timer(&clear_type, CLEAR_MS, false);
  set_timer(&main_timer_type, MAIN_MS, false);
  return PTS_VALUE_UNDEFINED;
}

static void set_unreferenced_timers_once_the_tasks_are_done(void) { set_timer(&poll_type, POLL_MS, false); }

static void unreferenced_timers_fired_while_the_program_ran(uint64_t ended_at) {
  CHECK(atomic_load(&unref_fired) == 1);
  CHECK(atomic_load(&unref_fired_by_main) == 1);
  CHECK(atomic_load(&unref_fired_at) - atomic_load(&phase_started_at) >= (uint64_t)(UNREF_MS - 10) * 1000000);
  CHECK(atomic_load(&ticks_by_main) >= 3);
  CHECK(atomic_load(&long_fired) == 0);
  CHECK(ended_at - atomic_load(&phase_started_at) < (uint64_t)10 * 1000000000); /* not the 10 minutes */
  for (int i = 0; i < RACING; i++) {
    CHECK(atomic_load(&racing_fired[i]) <= 1);
  }
}

/* The loops run once per program, so the tests share the one pts_run_loop. */
int main(int argc, char **argv) {
  setenv("PTS_THREADS", "4", 1);
  pts_runtime_init(argc, argv);
  pts_loop_init();
  post_two_calls_to_every_loop();
  clear_a_timer_from_another_loop();
  set_unreferenced_timers_once_the_tasks_are_done();
  tasks_on_every_loop_settle_their_promises_on_the_starting_loop();
  unreferenced_timers_fired_while_the_program_ran(uv_hrtime());
  calls_ran_on_their_loops_in_order();
  CHECK(atomic_load(&timer_fired) == 0);
  return CHECK_DONE();
}
