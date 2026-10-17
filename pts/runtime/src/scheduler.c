#include "pts.h"

#include "scheduler_internal.h"

#include <limits.h>
#include <pthread.h>
#include <sched.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <time.h>
#include <unistd.h>
#include <uv.h>

/*
 * The program's event loops (D-121): one per thread, the main thread's first, all over
 * one heap. Loops hand each other work through inboxes (pts_message): tasks to start,
 * promises to settle, reactions to run. A loop that runs out of work takes a task that
 * another loop has queued but not started, or else sleeps until a message wakes it; once
 * every loop sleeps, nothing is left that could wake one, and the program is done. The
 * loops other than the main thread's start the first time `parallel` runs, and each task
 * goes to the least-loaded of them.
 */

enum { MAX_LOOPS = 256 };
/* A wake-up starts at most this many tasks, so that a flood of them doesn't hold up the loop's timers and I/O. */
enum { TASKS_PER_WAKE = 64 };
/* A loop thread's stack, where the main thread's has no limit. */
enum { DEFAULT_STACK_SIZE = 8 << 20 };

typedef struct loop_slot {
  uv_loop_t *uv;
  uv_async_t *wake;     /* what other threads send to, to make the loop read its inbox */
  uv_prepare_t *flush;  /* flushes stdout before the loop waits for I/O */
  pthread_mutex_t lock; /* guards the inbox and sleeping */
  pthread_cond_t woken;
  pts_message *first, *last;           /* messages, in the order they came */
  pts_message *first_task, *last_task; /* tasks no loop has started */
  atomic_size_t queued;                /* tasks waiting there: what a loop looking for one to take reads without the lock */
  atomic_size_t load;                  /* tasks placed on the loop and not finished */
  bool sleeping;                       /* waiting for a message, and not counted awake */
} loop_slot;

/* The loops, the main thread's first. Loops never stop, so a slot lives as long as the program; slots are uncollectable, so the collector scans their inboxes. */
static loop_slot *slots[MAX_LOOPS];
static atomic_uint loop_count = 1;
static pthread_once_t workers_once = PTHREAD_ONCE_INIT;
static pthread_once_t main_slot_once = PTHREAD_ONCE_INIT;
/* Loops that aren't sleeping. A loop that wakes another counts it awake before letting it go, so the count can't reach 0 while work is in flight. */
static atomic_size_t awake = 1;
static atomic_bool finished;    /* every loop slept at once: the program is done */
static atomic_uint next_choice; /* where the search for the least-loaded loop starts, round-robin among equals */

_Thread_local uint16_t pts_current_loop;
_Thread_local uint32_t pts_parallel_nesting;
bool pts_threaded;
static _Thread_local loop_slot *own;
static _Thread_local uint16_t own_index; /* own's index in slots */

static void *checked(void *memory) {
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory starting an event loop");
  }
  return memory;
}

static void on_wake(uv_async_t *handle);

/* Before a loop waits for I/O, what the program printed goes out: a server's log lines appear as it runs, as Node's do (D-136). */
static void on_prepare(uv_prepare_t *handle) {
  (void)handle;
  fflush(stdout);
}

static loop_slot *new_slot(void) {
  loop_slot *slot = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *slot));
  slot->uv = checked(malloc(sizeof *slot->uv));
  slot->wake = checked(malloc(sizeof *slot->wake));
  if (uv_loop_init(slot->uv) != 0 || uv_async_init(slot->uv, slot->wake, on_wake) != 0) {
    pts_panic("Error", "Cannot start an event loop");
  }
  slot->wake->data = slot;
  uv_unref((uv_handle_t *)slot->wake); /* waiting for messages doesn't keep a loop running: it sleeps until one comes */
  slot->flush = checked(malloc(sizeof *slot->flush));
  uv_prepare_init(slot->uv, slot->flush);
  uv_prepare_start(slot->flush, on_prepare);
  uv_unref((uv_handle_t *)slot->flush);
  pthread_mutex_init(&slot->lock, NULL);
  pthread_condattr_t attributes;
  pthread_condattr_init(&attributes);
  pthread_condattr_setclock(&attributes, CLOCK_MONOTONIC); /* libuv's clock: a sleeping loop waits for its timers (wait_for_work) */
  pthread_cond_init(&slot->woken, &attributes);
  pthread_condattr_destroy(&attributes);
  return slot;
}

static void make_main_slot(void) { slots[0] = new_slot(); }

/* This thread's loop; the main thread's is made the first time it is needed. A pool worker has none (D-122). */
static loop_slot *own_slot(void) {
  if (own == NULL) {
    if (pts_current_loop != 0) {
      pts_throw_new(&pts_type_builtin_Error, "Timers and asynchronous I/O can't be started from a parallel operation's callback yet");
    }
    pthread_once(&main_slot_once, make_main_slot);
    own = slots[0];
  }
  return own;
}

uv_loop_t *pts_uv_loop(void) { return own_slot()->uv; }

uint16_t pts_own_loop(void) { return own_index; }

/* ---------------------------------------------------------------- inboxes */

static void append(pts_message **first, pts_message **last, pts_message *message) {
  if (*last != NULL) {
    (*last)->next = message;
  } else {
    *first = message;
  }
  *last = message;
}

void pts_post(uint16_t loop, pts_message *message) {
  loop_slot *slot = slots[loop];
  message->next = NULL;
  pthread_mutex_lock(&slot->lock);
  if (message->task) {
    append(&slot->first_task, &slot->last_task, message);
    atomic_fetch_add(&slot->queued, 1);
  } else {
    append(&slot->first, &slot->last, message);
  }
  bool sleeping = slot->sleeping;
  if (sleeping) {
    slot->sleeping = false;
    atomic_fetch_add(&awake, 1);
    pthread_cond_signal(&slot->woken);
  }
  pthread_mutex_unlock(&slot->lock);
  if (!sleeping) {
    uv_async_send(slot->wake); /* it may be waiting in its loop's poll */
  }
}

/* The first task in slot's inbox, or NULL; the caller holds slot's lock. */
static pts_message *first_task(loop_slot *slot) {
  pts_message *task = slot->first_task;
  if (task != NULL) {
    slot->first_task = task->next;
    if (slot->first_task == NULL) {
      slot->last_task = NULL;
    }
    atomic_fetch_sub(&slot->queued, 1);
  }
  return task;
}

/* The first message in slot's inbox, else its first task, else NULL. */
static pts_message *take(loop_slot *slot) {
  pthread_mutex_lock(&slot->lock);
  pts_message *message = slot->first;
  if (message != NULL) {
    slot->first = message->next;
    if (slot->first == NULL) {
      slot->last = NULL;
    }
  } else {
    message = first_task(slot);
  }
  pthread_mutex_unlock(&slot->lock);
  return message;
}

void pts_absorb_messages(void) {
  if (own == NULL) {
    return;
  }
  for (;;) {
    pthread_mutex_lock(&own->lock);
    pts_message *message = own->first;
    if (message != NULL) {
      own->first = message->next;
      if (own->first == NULL) {
        own->last = NULL;
      }
    }
    pthread_mutex_unlock(&own->lock);
    if (message == NULL) {
      return;
    }
    pts_message_apply(message);
  }
}

/* Runs what is in slot's inbox, up to TASKS_PER_WAKE tasks; with more, the loop wakes itself to go on after its other callbacks. */
static void run_inbox(loop_slot *slot) {
  size_t tasks = 0;
  pts_message *message;
  while (tasks < TASKS_PER_WAKE && (message = take(slot)) != NULL) {
    tasks += message->task;
    pts_message_run(message);
  }
  if (tasks == TASKS_PER_WAKE) {
    uv_async_send(slot->wake);
  }
}

static void on_wake(uv_async_t *handle) { run_inbox(handle->data); }

/* A task that another loop has queued but not started, which thief takes over, or NULL. */
static pts_message *steal(loop_slot *thief) {
  unsigned count = atomic_load(&loop_count);
  for (unsigned i = 1; i < count; i++) {
    loop_slot *victim = slots[(pts_current_loop + i) % count];
    if (atomic_load_explicit(&victim->queued, memory_order_relaxed) == 0) {
      continue;
    }
    pthread_mutex_lock(&victim->lock);
    pts_message *task = first_task(victim);
    pthread_mutex_unlock(&victim->lock);
    if (task != NULL) {
      atomic_fetch_sub(&victim->load, 1);
      atomic_fetch_add(&thief->load, 1);
      return task;
    }
  }
  return NULL;
}

/* ---------------------------------------------------------------- sleeping and running */

static void wake_main(void) {
  loop_slot *main_slot = slots[0];
  pthread_mutex_lock(&main_slot->lock);
  pthread_cond_signal(&main_slot->woken);
  pthread_mutex_unlock(&main_slot->lock);
}

/* Waits for slot's condition for at most ms milliseconds; the caller holds slot's lock. */
static void wait_at_most(loop_slot *slot, int64_t ms) {
  struct timespec until;
  clock_gettime(CLOCK_MONOTONIC, &until);
  until.tv_sec += ms / 1000;
  until.tv_nsec += (ms % 1000) * 1000000;
  if (until.tv_nsec >= 1000000000) {
    until.tv_sec++;
    until.tv_nsec -= 1000000000;
  }
  pthread_cond_timedwait(&slot->woken, &slot->lock, &until);
}

/* Counts a sleeping loop awake again for its timer, unless no loop is awake: then the program is ending, which an unreferenced timer doesn't hold up. */
static bool rejoin(void) {
  size_t count = atomic_load(&awake);
  while (count > 0 && !atomic_compare_exchange_weak(&awake, &count, count + 1)) {
  }
  return count > 0;
}

/*
 * Sleeps until a message comes, unless one has, or until the first of the loop's timers
 * (all unreferenced) is due while the program still runs: true when there is work, false
 * when every loop sleeps instead. The last loop to fall asleep ends the program by waking
 * the main thread's, whose pts_run_loop then returns.
 */
static bool wait_for_work(loop_slot *slot) {
  pthread_mutex_lock(&slot->lock);
  if (slot->first == NULL && slot->first_task == NULL) {
    slot->sleeping = true;
    if (atomic_fetch_sub(&awake, 1) == 1) {
      atomic_store(&finished, true);
      pthread_mutex_unlock(&slot->lock);
      wake_main();
      return false;
    }
    bool ending = false; /* a timer came due when no loop was awake */
    while (slot->sleeping && !atomic_load(&finished)) {
      int64_t wait = ending ? -1 : pts_loop_timer_wait();
      if (wait < 0) {
        pthread_cond_wait(&slot->woken, &slot->lock);
      } else if (wait > 0) {
        wait_at_most(slot, wait);
      } else if (rejoin()) {
        slot->sleeping = false;
      } else {
        ending = true;
      }
    }
  }
  bool working = !slot->sleeping;
  pthread_mutex_unlock(&slot->lock);
  return working;
}

/*
 * Runs slot's loop until the program is done: its libuv loop until nothing referenced is
 * left, then a task taken from another loop, or else the timers and messages it sleeps
 * until. libuv runs no timers while nothing referenced is left, so the loop runs those
 * that are due itself. After that work, as after I/O, libuv's phases go on from poll, so
 * the immediates it queued run before the next timers. What the program printed goes out
 * before the loop sleeps, as before it polls.
 */
static void run(loop_slot *slot) {
  uv_run(slot->uv, UV_RUN_DEFAULT);
  for (;;) {
    pts_message *task = steal(slot);
    if (task != NULL) {
      pts_message_run(task);
    } else {
      fflush(stdout);
      if (!wait_for_work(slot)) {
        return;
      }
      pts_loop_run_due_timers();
      run_inbox(slot);
    }
    if (uv_run(slot->uv, UV_RUN_NOWAIT) == 0) {
      pts_loop_run_due_timers(); /* nothing referenced, so no immediate waits to go first */
    }
    while (uv_run(slot->uv, UV_RUN_ONCE) != 0) {
      /* UV_RUN_DEFAULT without its first timers phase */
    }
  }
}

void pts_run_loop(void) { run(own_slot()); }

/* ---------------------------------------------------------------- the other loops and tasks */

static void *run_worker(void *index) {
  pts_current_loop = own_index = (uint16_t)(uintptr_t)index;
  own = slots[pts_current_loop];
  pts_stack_limit_init();
  run(own);
  for (;;) {
    pause(); /* the program is done and the main thread is ending it: no thread teardown races its exit */
  }
}

static unsigned configured_count(void) {
  const char *text = getenv("PTS_THREADS");
  long count = 0;
  if (text != NULL) {
    char *end;
    count = strtol(text, &end, 10);
    if (end == text || *end != '\0') {
      count = 0;
    }
  }
  if (count < 1) {
    cpu_set_t cpus;
    count = sched_getaffinity(0, sizeof cpus, &cpus) == 0 ? CPU_COUNT(&cpus) : sysconf(_SC_NPROCESSORS_ONLN);
  }
  return count < 1 ? 1 : count > MAX_LOOPS ? MAX_LOOPS : (unsigned)count;
}

static unsigned threads;
static pthread_once_t threads_once = PTHREAD_ONCE_INIT;

static void configure_threads(void) { threads = configured_count(); }

unsigned pts_configured_threads(void) {
  pthread_once(&threads_once, configure_threads);
  return threads;
}

/* A loop thread's stack size: the main thread's limit, as Node's and Go's threads take. */
static size_t stack_size(void) {
  struct rlimit limit;
  if (getrlimit(RLIMIT_STACK, &limit) == 0 && limit.rlim_cur != RLIM_INFINITY && limit.rlim_cur >= PTHREAD_STACK_MIN) {
    return limit.rlim_cur;
  }
  return DEFAULT_STACK_SIZE;
}

/* Starts the loops other than the main thread's, each counted awake until it first runs dry; a thread the system won't start leaves the program with fewer loops. */
static void start_workers(void) {
  pthread_once(&main_slot_once, make_main_slot); /* not own_slot: a pool worker's first `parallel` may start the loops, and a throw can't leave a pthread_once */
  unsigned count = pts_configured_threads();
  for (unsigned i = 1; i < count; i++) {
    slots[i] = new_slot();
  }
  if (count > 1) {
    __atomic_store_n(&pts_threaded, true, __ATOMIC_RELAXED); /* pthread_create publishes it */
  }
  pthread_attr_t attributes;
  pthread_attr_init(&attributes);
  pthread_attr_setdetachstate(&attributes, PTHREAD_CREATE_DETACHED);
  pthread_attr_setstacksize(&attributes, stack_size());
  unsigned started = 1;
  for (; started < count; started++) {
    atomic_fetch_add(&awake, 1);
    pthread_t thread;
    if (pthread_create(&thread, &attributes, run_worker, (void *)(uintptr_t)started) != 0) {
      atomic_fetch_sub(&awake, 1);
      break;
    }
  }
  pthread_attr_destroy(&attributes);
  atomic_store(&loop_count, started);
}

unsigned pts_start_loops(void) {
  pthread_once(&workers_once, start_workers);
  return atomic_load(&loop_count);
}

uint16_t pts_place_task(void) {
  unsigned count = pts_start_loops();
  uint16_t self = pts_current_loop & ~PTS_LOOP_FOREIGN;
  uint16_t best = self;
  size_t best_load = SIZE_MAX;
  unsigned start = atomic_fetch_add_explicit(&next_choice, 1, memory_order_relaxed);
  for (unsigned i = 0; i < count && count > 1; i++) {
    uint16_t candidate = (uint16_t)((start + i) % count);
    size_t load = atomic_load_explicit(&slots[candidate]->load, memory_order_relaxed);
    if (candidate != self && load < best_load) {
      best = candidate;
      best_load = load;
    }
  }
  atomic_fetch_add(&slots[best]->load, 1);
  return best;
}

void pts_task_finished(void) { atomic_fetch_sub(&own_slot()->load, 1); }

bool pts_parallel_on_main_thread(void) { return pts_current_loop == 0 && pts_parallel_nesting == 0; }
