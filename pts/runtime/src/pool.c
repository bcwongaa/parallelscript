#include "pts.h"

#include "scheduler_internal.h"

#include <pthread.h>
#include <sched.h>
#include <stdalign.h>
#include <stdatomic.h>
#include <stddef.h>
#include <string.h>

/*
 * The fork-join pool (D-122), after Rayon: worker threads apart from the loops, each
 * with a Chase–Lev deque of jobs. pts_join pushes its second closure as a job on the
 * calling thread's deque and runs the first; if no thread stole the job meanwhile, it
 * runs it too, else it runs other jobs until the thief is done. A thread with nothing to
 * run steals the oldest job of another thread's deque. A loop thread that starts a
 * parallel operation gets a deque too and helps like a worker until it is done, so the
 * pool has one worker fewer than the threads operations run on. Jobs live on the stack
 * of the join that made them, which returns only once they are done.
 */

/* Deques: the pool's workers, then the loop threads that started parallel operations. */
enum { MAX_DEQUES = 512 };
/* How many times a thread with nothing to run looks again, yielding in between, before it sleeps. */
enum { SPINS_BEFORE_SLEEP = 64 };
enum { INITIAL_DEQUE_SIZE = 64 };

typedef struct pool_job pool_job;
struct pool_job {
  void (*execute)(pool_job *job);
  uint16_t loop; /* the loop whose parallel operation it is part of */
};

/* What a thief that lost a race for a job returns, as opposed to NULL for an empty deque. */
static pool_job contended;
#define CONTENDED (&contended)

typedef struct buffer {
  int64_t mask; /* its size - 1, a power of two minus 1 */
  _Atomic(pool_job *) slots[];
} buffer;

/* The deque of Lê, Pop, Cohen and Zappa Nardelli's "Correct and Efficient Work-Stealing for Weak Memory Models". */
typedef struct deque {
  atomic_int_least64_t top; /* thieves take here */
  char top_line[64 - sizeof(atomic_int_least64_t)];
  atomic_int_least64_t bottom; /* the owner pushes and pops here */
  _Atomic(buffer *) array;     /* collected: a thief may still read the one a push replaced */
} deque;

/* A thread that runs jobs: a pool worker, or a loop thread inside a parallel operation. */
typedef struct worker {
  deque *jobs;
  int32_t own_loop; /* the loop whose thread it is; -1 for a pool worker */
  uint64_t random;  /* where it starts looking for a job to steal */
} worker;

static deque *deques[MAX_DEQUES];
static atomic_uint deque_count; /* raised (release) only once deques[] holds the new one */
static pthread_mutex_t deques_lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_once_t pool_once = PTHREAD_ONCE_INIT;
static unsigned pool_threads = 1; /* the threads operations run on: the workers and the caller */
static _Thread_local worker *self;

/* Sleeping (Rayon's protocol): a thread about to sleep counts itself sleepy, looks for work once more, and sleeps until the event count moves; whoever makes work or finishes a stolen job moves it if anyone is sleepy. */
static pthread_mutex_t sleep_lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t sleep_changed = PTHREAD_COND_INITIALIZER;
static atomic_uint sleepy;
static atomic_ulong events;

static void *checked(void *memory) {
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory in the fork-join pool");
  }
  return memory;
}

/* ---------------------------------------------------------------- deques */

static buffer *new_buffer(int64_t size) {
  buffer *array = checked(GC_MALLOC(sizeof(buffer) + (size_t)size * sizeof(_Atomic(pool_job *))));
  array->mask = size - 1;
  return array;
}

static deque *new_deque(void) {
  deque *jobs = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *jobs));
  atomic_store(&jobs->array, new_buffer(INITIAL_DEQUE_SIZE));
  return jobs;
}

/* A buffer twice the size, holding the jobs from top to bottom; the old one stays for thieves still reading it. */
static buffer *grow(deque *jobs, buffer *old, int64_t top, int64_t bottom) {
  buffer *array = new_buffer(2 * (old->mask + 1));
  for (int64_t i = top; i < bottom; i++) {
    atomic_store_explicit(&array->slots[i & array->mask], atomic_load_explicit(&old->slots[i & old->mask], memory_order_relaxed), memory_order_relaxed);
  }
  atomic_store_explicit(&jobs->array, array, memory_order_release);
  return array;
}

/* The owner adds a job at the bottom. */
static void push(deque *jobs, pool_job *job) {
  int64_t bottom = atomic_load_explicit(&jobs->bottom, memory_order_relaxed);
  int64_t top = atomic_load_explicit(&jobs->top, memory_order_acquire);
  buffer *array = atomic_load_explicit(&jobs->array, memory_order_relaxed);
  if (bottom - top > array->mask) {
    array = grow(jobs, array, top, bottom);
  }
  atomic_store_explicit(&array->slots[bottom & array->mask], job, memory_order_relaxed);
  atomic_store_explicit(&jobs->bottom, bottom + 1, memory_order_release);
}

/* The owner takes the newest job back, or NULL if thieves took them all. */
static pool_job *pop(deque *jobs) {
  int64_t bottom = atomic_load_explicit(&jobs->bottom, memory_order_relaxed) - 1;
  buffer *array = atomic_load_explicit(&jobs->array, memory_order_relaxed);
  atomic_store_explicit(&jobs->bottom, bottom, memory_order_relaxed);
  atomic_thread_fence(memory_order_seq_cst);
  int64_t top = atomic_load_explicit(&jobs->top, memory_order_relaxed);
  if (top > bottom) {
    atomic_store_explicit(&jobs->bottom, bottom + 1, memory_order_relaxed);
    return NULL;
  }
  pool_job *job = atomic_load_explicit(&array->slots[bottom & array->mask], memory_order_relaxed);
  if (top == bottom) { /* the last one: a thief may be taking it */
    if (!atomic_compare_exchange_strong_explicit(&jobs->top, &top, top + 1, memory_order_seq_cst, memory_order_relaxed)) {
      job = NULL;
    }
    atomic_store_explicit(&jobs->bottom, bottom + 1, memory_order_relaxed);
  }
  return job;
}

/* A thief takes the oldest job: NULL if there is none, CONTENDED if another thread took it first. */
static pool_job *steal(deque *jobs) {
  int64_t top = atomic_load_explicit(&jobs->top, memory_order_acquire);
  atomic_thread_fence(memory_order_seq_cst);
  int64_t bottom = atomic_load_explicit(&jobs->bottom, memory_order_acquire);
  if (top >= bottom) {
    return NULL;
  }
  buffer *array = atomic_load_explicit(&jobs->array, memory_order_acquire);
  pool_job *job = atomic_load_explicit(&array->slots[top & array->mask], memory_order_relaxed);
  if (!atomic_compare_exchange_strong_explicit(&jobs->top, &top, top + 1, memory_order_seq_cst, memory_order_relaxed)) {
    return CONTENDED;
  }
  return job;
}

/* ---------------------------------------------------------------- finding, running and waiting for jobs */

/* Wakes the sleeping threads if any thread is sleepy: there is a new job, or a job they may wait for is done. */
static void notify(void) {
  atomic_thread_fence(memory_order_seq_cst);
  if (atomic_load_explicit(&sleepy, memory_order_relaxed) > 0) {
    pthread_mutex_lock(&sleep_lock);
    atomic_fetch_add(&events, 1);
    pthread_cond_broadcast(&sleep_changed);
    pthread_mutex_unlock(&sleep_lock);
  }
}

static uint64_t next_random(worker *w) {
  w->random ^= w->random << 13;
  w->random ^= w->random >> 7;
  w->random ^= w->random << 17;
  return w->random;
}

/* A job stolen from another thread's deque, trying them all from a random one, or NULL if none has any. */
static pool_job *steal_any(worker *w) {
  for (;;) {
    bool lost = false;
    unsigned count = atomic_load_explicit(&deque_count, memory_order_acquire);
    unsigned start = (unsigned)(next_random(w) % count);
    for (unsigned i = 0; i < count; i++) {
      deque *victim = deques[(start + i) % count];
      if (victim == w->jobs) {
        continue;
      }
      pool_job *job = steal(victim);
      if (job == CONTENDED) {
        lost = true;
      } else if (job != NULL) {
        return job;
      }
    }
    if (!lost) {
      return NULL;
    }
  }
}

/* Runs a job for its loop: as that loop if this is its thread, else as a foreign thread working for it. */
static void execute(worker *w, pool_job *job) {
  uint16_t saved = pts_current_loop;
  pts_current_loop = w->own_loop == job->loop ? job->loop : job->loop | PTS_LOOP_FOREIGN;
  job->execute(job);
  pts_current_loop = saved;
}

static bool is_set(atomic_bool *done) { return done != NULL && atomic_load_explicit(done, memory_order_acquire); }

/* Sleeps until a job may be there to run or done is set, unless the last look for one finds it. */
static void sleep_until_changed(worker *w, atomic_bool *done) {
  atomic_fetch_add(&sleepy, 1);
  atomic_thread_fence(memory_order_seq_cst); /* pairs with notify's: it sees this thread sleepy, or this thread sees its work */
  unsigned long seen = atomic_load(&events);
  pool_job *job = steal_any(w);
  if (job == NULL && !is_set(done)) {
    pthread_mutex_lock(&sleep_lock);
    while (atomic_load(&events) == seen && !is_set(done)) {
      pthread_cond_wait(&sleep_changed, &sleep_lock);
    }
    pthread_mutex_unlock(&sleep_lock);
  }
  atomic_fetch_sub(&sleepy, 1);
  if (job != NULL) {
    execute(w, job);
  }
}

/* Runs jobs, its own first, then other threads', until done is set (forever if it is NULL), sleeping while there are none. */
static void work_until(worker *w, atomic_bool *done) {
  unsigned idle = 0;
  while (!is_set(done)) {
    pool_job *job = pop(w->jobs);
    if (job == NULL) {
      job = steal_any(w);
    }
    if (job != NULL) {
      execute(w, job);
      idle = 0;
    } else if (++idle < SPINS_BEFORE_SLEEP) {
      sched_yield();
    } else {
      sleep_until_changed(w, done);
      idle = 0;
    }
  }
}

/* ---------------------------------------------------------------- the pool's threads */

static void add_deque(worker *w) {
  pthread_mutex_lock(&deques_lock);
  unsigned index = atomic_load_explicit(&deque_count, memory_order_relaxed);
  if (index >= MAX_DEQUES) {
    pts_panic("Error", "Too many threads in the fork-join pool");
  }
  deques[index] = w->jobs;
  atomic_store_explicit(&deque_count, index + 1, memory_order_release);
  pthread_mutex_unlock(&deques_lock);
}

static worker *new_worker(int32_t own_loop) {
  worker *w = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *w));
  w->jobs = new_deque();
  w->own_loop = own_loop;
  w->random = 0x9E3779B97F4A7C15u ^ (uint64_t)(uintptr_t)w;
  return w;
}

static void *run_pool_worker(void *argument) {
  self = argument;
  pts_current_loop = PTS_LOOP_FOREIGN; /* until it runs a job, which says whose */
  pts_stack_limit_init();
  work_until(self, NULL);
  return NULL;
}

/* Starts the workers: one fewer than the threads operations run on, since the thread that starts one helps. */
static void start_pool(void) {
  unsigned count = pts_configured_threads();
  if (count <= 1) {
    return;
  }
  worker *workers[MAX_DEQUES];
  for (unsigned i = 0; i + 1 < count; i++) {
    workers[i] = new_worker(-1);
    add_deque(workers[i]);
  }
  __atomic_store_n(&pts_threaded, true, __ATOMIC_RELAXED); /* pthread_create publishes it */
  pthread_attr_t attributes;
  pthread_attr_init(&attributes);
  pthread_attr_setdetachstate(&attributes, PTHREAD_CREATE_DETACHED);
  unsigned started = 0;
  for (; started + 1 < count; started++) {
    pthread_t thread;
    if (pthread_create(&thread, &attributes, run_pool_worker, workers[started]) != 0) {
      break; /* the deques of workers that didn't start stay empty */
    }
  }
  pthread_attr_destroy(&attributes);
  pool_threads = started + 1;
}

/* This thread as one that runs jobs, given a deque the first time; NULL with no pool (PTS_THREADS=1). */
static worker *current_worker(void) {
  pthread_once(&pool_once, start_pool);
  if (self == NULL && pool_threads > 1) {
    self = new_worker(pts_current_loop);
    add_deque(self);
  }
  return self;
}

unsigned pts_pool_threads(void) {
  pthread_once(&pool_once, start_pool);
  return pool_threads;
}

/* ---------------------------------------------------------------- join */

/* Runs function(argument, migrated), catching what it throws: true and the exception, or false. */
static bool run_catching(pts_join_function function, void *argument, bool migrated, pts_value *exception) {
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) != 0) {
    *exception = pts_caught(&handler);
    return true;
  }
  function(argument, migrated);
  pts_try_leave(&handler);
  return false;
}

/* pts_join's second closure, as a job other threads may steal. */
typedef struct join_job {
  pool_job header;
  pts_join_function function;
  void *argument;
  atomic_bool done; /* set by the thief once it has run */
  bool threw;
  pts_value exception;
} join_job;

static void run_stolen(pool_job *header) {
  join_job *job = (join_job *)header;
  job->threw = run_catching(job->function, job->argument, true, &job->exception);
  atomic_store_explicit(&job->done, true, memory_order_release);
  notify(); /* job may be gone now: its join can return */
}

void pts_join(pts_join_function a, void *a_argument, pts_join_function b, void *b_argument) {
  worker *w = current_worker();
  pts_value a_exception, b_exception;
  bool a_threw, b_threw;
  if (w == NULL) {
    a_threw = run_catching(a, a_argument, false, &a_exception);
    b_threw = run_catching(b, b_argument, false, &b_exception);
  } else {
    join_job job = {.header = {.execute = run_stolen, .loop = pts_current_loop & ~PTS_LOOP_FOREIGN}, .function = b, .argument = b_argument};
    push(w->jobs, &job.header);
    notify();
    a_threw = run_catching(a, a_argument, false, &a_exception);
    for (;;) {
      if (is_set(&job.done)) {
        b_threw = job.threw;
        b_exception = job.exception;
        break;
      }
      pool_job *next = pop(w->jobs);
      if (next == &job.header) { /* not stolen: the common case */
        b_threw = run_catching(b, b_argument, false, &b_exception);
        break;
      }
      if (next != NULL) {
        execute(w, next);
      } else {
        work_until(w, &job.done);
      }
    }
  }
  if (a_threw) {
    pts_throw(a_exception);
  }
  if (b_threw) {
    pts_throw(b_exception);
  }
}

/* ---------------------------------------------------------------- adaptive splitting */

/* A piece of an operation: the indices from..to, the splits it may still make, and where its result goes. */
typedef struct part {
  const pts_par_op *op;
  size_t from, to;
  size_t splits;
  void *result;
} part;

/* Rayon's adaptive splitter: a piece that moved to another thread found idle threads there, so it may split as much as at the start again; one that didn't halves its budget. */
static bool try_split(size_t *splits, bool migrated) {
  if (migrated) {
    *splits = *splits / 2 > pool_threads ? *splits / 2 : pool_threads;
    return true;
  }
  if (*splits > 0) {
    *splits /= 2;
    return true;
  }
  return false;
}

static void run_part(void *argument, bool migrated) {
  part *piece = argument;
  const pts_par_op *op = piece->op;
  size_t length = piece->to - piece->from;
  size_t min_length = op->min_length > 0 ? op->min_length : 1;
  if (length / 2 < min_length || !try_split(&piece->splits, migrated)) {
    op->leaf(op, piece->from, piece->to, piece->result);
    return;
  }
  size_t middle = piece->from + length / 2;
  max_align_t right_result[op->result_size / sizeof(max_align_t) + 1];
  memset(right_result, 0, op->result_size);
  part left = {op, piece->from, middle, piece->splits, piece->result};
  part right = {op, middle, piece->to, piece->splits, right_result};
  pts_join(run_part, &left, run_part, &right);
  if (op->combine != NULL) {
    op->combine(op, piece->result, right_result);
  }
}

void pts_par_run(const pts_par_op *op, size_t count, void *result) {
  if (op->result_size > 0) {
    memset(result, 0, op->result_size);
  }
  if (pts_pool_threads() == 1) {
    op->leaf(op, 0, count, result); /* sequentially on the caller (D-O06) */
    return;
  }
  part root = {op, 0, count, pool_threads, result};
  run_part(&root, false);
}

/* ---------------------------------------------------------------- pts:parallel's intrinsics (D-123) */

/* An operation whose pieces call the library's closures: leaf(from, to), and combine(left, right) for a reduction. */
typedef struct closure_op {
  pts_par_op op;
  pts_closure *leaf;
  pts_closure *combine;
} closure_op;

/* A count of indices from a number: its integer part, 0 if it isn't positive. */
static size_t index_count(double count) { return count >= 1 ? (size_t)count : 0; }

static void call_closure(void *closure, bool migrated) {
  (void)migrated;
  ((void (*)(pts_closure *))((pts_closure *)closure)->code)(closure);
}

/*
 * Runs an operation for the library: on the loop's own thread, the loop's inbox exists
 * first (callbacks on other threads post their promise work to it), and that work is
 * done before the operation returns or throws.
 */
static void operate(const pts_par_op *op, size_t count, void *result, bool join) {
  bool on_loop = !(pts_current_loop & PTS_LOOP_FOREIGN);
  if (on_loop) {
    pts_uv_loop();
  }
  pts_handler handler;
  pts_try_enter(&handler);
  pts_parallel_nesting++;
  if (_setjmp(handler.jump) != 0) {
    pts_parallel_nesting--;
    if (on_loop) {
      pts_absorb_messages();
    }
    pts_throw(pts_caught(&handler));
  }
  if (join) {
    const closure_op *pair = (const closure_op *)op;
    pts_join(call_closure, pair->leaf, call_closure, pair->combine);
  } else {
    pts_par_run(op, count, result);
  }
  pts_try_leave(&handler);
  pts_parallel_nesting--;
  if (on_loop) {
    pts_absorb_messages();
  }
}

static void for_leaf(const pts_par_op *op, size_t from, size_t to, void *result) {
  (void)result;
  pts_closure *leaf = ((const closure_op *)op)->leaf;
  ((void (*)(pts_closure *, double, double))leaf->code)(leaf, (double)from, (double)to);
}

void pts_parallel_for(pts_closure *leaf, double count) {
  closure_op op = {.op = {.leaf = for_leaf}, .leaf = leaf};
  operate(&op.op, index_count(count), NULL, false);
}

/* A `parallel for` (D-127): the leaf closure, whether a run of it has thrown, and the first error thrown. */
typedef struct for_each_op {
  closure_op base;
  bool *cancelled;
  pts_value *first;
} for_each_op;

static void for_each_leaf(const pts_par_op *op, size_t from, size_t to, void *result) {
  (void)result;
  const for_each_op *self = (const for_each_op *)op;
  pts_closure *leaf = self->base.leaf;
  for (size_t start = from; start < to && !__atomic_load_n(self->cancelled, __ATOMIC_RELAXED); start += PTS_PARALLEL_FOR_RUN) {
    size_t end = to - start > PTS_PARALLEL_FOR_RUN ? start + PTS_PARALLEL_FOR_RUN : to;
    pts_handler handler;
    pts_try_enter(&handler);
    if (_setjmp(handler.jump) != 0) {
      pts_value exception = pts_caught(&handler);
      bool expected = false;
      if (__atomic_compare_exchange_n(self->cancelled, &expected, true, false, __ATOMIC_ACQ_REL, __ATOMIC_RELAXED)) {
        *self->first = exception;
      }
      pts_throw(exception);
    }
    ((void (*)(pts_closure *, double, double))leaf->code)(leaf, (double)start, (double)end);
    pts_try_leave(&handler);
  }
}

void pts_parallel_for_each(pts_closure *leaf, double count) {
  bool cancelled = false;
  volatile pts_value first = 0;
  for_each_op op = {.base = {.op = {.leaf = for_each_leaf}, .leaf = leaf}, .cancelled = &cancelled, .first = (pts_value *)&first};
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) != 0) {
    pts_value caught = pts_caught(&handler);
    pts_throw(first != 0 ? first : caught); /* the error thrown first, not the lowest piece's */
  }
  operate(&op.base.op, index_count(count), NULL, false);
  pts_try_leave(&handler);
}

static void reduce_leaf(const pts_par_op *op, size_t from, size_t to, void *result) {
  pts_closure *leaf = ((const closure_op *)op)->leaf;
  *(pts_value *)result = ((pts_value(*)(pts_closure *, double, double))leaf->code)(leaf, (double)from, (double)to);
}

static void reduce_combine(const pts_par_op *op, void *left, void *right) {
  pts_closure *combine = ((const closure_op *)op)->combine;
  *(pts_value *)left = ((pts_value(*)(pts_closure *, pts_value, pts_value))combine->code)(combine, *(pts_value *)left, *(pts_value *)right);
}

pts_value pts_parallel_reduce(pts_closure *leaf, pts_closure *combine, double count) {
  closure_op op = {.op = {.leaf = reduce_leaf, .combine = reduce_combine, .result_size = sizeof(pts_value)}, .leaf = leaf, .combine = combine};
  volatile pts_value result = PTS_VALUE_UNDEFINED;
  operate(&op.op, index_count(count), (pts_value *)&result, false);
  return result;
}

void pts_parallel_join(pts_closure *a, pts_closure *b) {
  closure_op pair = {.leaf = a, .combine = b};
  operate(&pair.op, 0, NULL, true);
}

double pts_parallel_threads(void) { return (double)pts_pool_threads(); }
