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

/* PTS_THREADS if it is a positive number (at most 256), else the number of cores the process may run on. */
unsigned pts_configured_threads(void);

/* Hands message to the inbox of loop and wakes it. */
void pts_post(uint16_t loop, pts_message *message);
/* The loop a new task goes to (starting the other loops the first time): the least-loaded other one, or this one if there is no other. */
uint16_t pts_place_task(void);
/* A task that ran on this loop is done: it no longer counts towards the loop's load. */
void pts_task_finished(void);

/* Runs a message on this loop (promise.c), followed by the microtasks it queued. */
void pts_message_run(pts_message *message);

#endif /* PTS_SCHEDULER_INTERNAL_H */
