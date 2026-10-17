#ifndef PTS_SCHEDULER_INTERNAL_H
#define PTS_SCHEDULER_INTERNAL_H

#include "pts.h"

/*
 * What the scheduler (scheduler.c) and promises (promise.c) share (D-121). A message is
 * work for one loop: promise.c makes it, the scheduler hands it to the loop's inbox and
 * wakes the loop, and the loop runs it with pts_message_run. A task (`parallel e`) is a
 * message that another loop may take while no loop has started it.
 */
typedef struct pts_message {
  struct pts_message *next;
  bool task;
} pts_message;

/*
 * The loop this thread works for: its own index on a loop's thread (0 for the main
 * thread's). A thread running a job of another loop's parallel operation (D-122), a pool
 * worker or a loop helping another, works for that loop with PTS_LOOP_FOREIGN added: the
 * promises it makes belong to that loop, and whatever it does to a promise is a message
 * to the promise's loop, since the index never matches.
 */
extern _Thread_local uint16_t pts_current_loop;
enum { PTS_LOOP_FOREIGN = 0x8000 };

/* The loop whose thread this is, whichever loop's work it does (0 on the main thread); only for a thread that has a loop (pts_uv_loop). */
uint16_t pts_own_loop(void);

/* How many `parallel` tasks' closures (promise.c) and parallel operations (pool.c) this thread is running, for pts_parallel_on_main_thread. */
extern _Thread_local uint32_t pts_parallel_nesting;

/*
 * Set before a second thread may run the program's code (the loops, D-121; the pool,
 * D-122). Until then, state that threads could share needs no locked instructions; a
 * thread that reads it false is the only one running the program (D-143).
 */
extern bool pts_threaded;

/* PTS_THREADS if it is a positive number (at most 256), else the number of cores the process may run on. */
unsigned pts_configured_threads(void);

/* Hands message to the inbox of loop and wakes it. */
void pts_post(uint16_t loop, pts_message *message);
/* Runs call(data) on loop, in order with the other messages it gets, followed by the microtasks it queues (D-137). The message keeps data alive. */
void pts_post_call(uint16_t loop, void (*call)(void *data), void *data);
/* Starts the loops other than the main thread's, if they haven't started, and returns how many loops there are. */
unsigned pts_start_loops(void);
/* The loop a new task goes to (starting the other loops the first time): the least-loaded other one, or this one if there is no other. */
uint16_t pts_place_task(void);
/* A task that ran on this loop is done: it no longer counts towards the loop's load. */
void pts_task_finished(void);

/*
 * For a loop that sleeps (C120): only unreferenced timers can be left in its heap, and its
 * libuv loop runs no timers while nothing referenced is left (loop.c).
 * pts_loop_timer_wait: how many ms until this thread's first timer is due (0 if it is),
 * reading the clock; -1 with no timers.
 * pts_loop_run_due_timers: runs the timers that are due, each followed by its microtasks.
 */
int64_t pts_loop_timer_wait(void);
void pts_loop_run_due_timers(void);

/* Runs a message on this loop (promise.c), followed by the microtasks it queued. */
void pts_message_run(pts_message *message);
/* Runs a message on this loop without the microtasks it queues (promise.c). */
void pts_message_apply(pts_message *message);
/*
 * After a parallel operation on this loop's thread (D-123): applies the messages (not
 * tasks) waiting in its inbox, so the promise work the operation's callbacks did on other
 * threads is done, and their microtasks queued, before the operation returns.
 */
void pts_absorb_messages(void);

#endif /* PTS_SCHEDULER_INTERNAL_H */
