#include "pts.h"

#include "dynamic_internal.h"
#include "scheduler_internal.h"

#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>

/*
 * Promises and microtasks (D-112): ECMAScript's promise algorithms in V8's order of jobs
 * (an await on a promise reacts to it directly, without a then() of its own), over a
 * queue of jobs per thread that pts_run_microtasks drains. Handlers the program passes are
 * called through their dynamic entries (D-074); the functions the algorithms make
 * (resolving functions, finally's handlers) are runtime closures that typed code reaches
 * through adapters, as it reaches a function from `any` (D-075).
 *
 * With more than one loop (D-121), a promise belongs to the loop that made it (its home)
 * and settles only there, and a reaction runs on the loop that added it (its owner):
 * settling a promise of another loop, or adding a reaction to one, is a message to its
 * home, and a reaction of another loop is a message to its owner. Each promise thus
 * changes on one thread only, and an async function resumes on the loop it started on.
 * An async generator belongs to the loop that made it in the same way: requests from
 * other loops are messages to it, so its queue and body run on that loop (D-143).
 */

enum { PENDING, FULFILLED, REJECTED };

struct pts_reaction;

struct pts_promise {
  const pts_type *type;
  int32_t state;
  bool handled;  /* [[PromiseIsHandled]]: a reaction was ever added; other loops set it atomically (D-143) */
  uint16_t home; /* the loop it settles on */
  pts_value result;
  struct pts_reaction *first, *last; /* while pending, in the order they were added */
};

const pts_type pts_type_builtin_Promise = {
    .kind = PTS_KIND_PROMISE,
    .gc_descr = PTS_GC_NO_POINTERS, /* allocated with GC_MALLOC and scanned whole */
    .name = "Promise",
    .size = sizeof(pts_promise),
};

/*
 * What happens when a promise settles: a then()'s handler runs and its outcome settles
 * the promise then() made (derived); an async function suspended at an await resumes
 * (awaiting); or a promise that was resolved with this one takes on its outcome (adopter).
 * Once its promise settles, a reaction is a job in the microtask queue, run with the
 * promise's state and result; a job that follows a thenable (follower) is one too. A
 * reaction is in one list at a time: its promise's while it is pending, then the queue.
 * A task's value settling settles the task's promise (task) on that promise's loop.
 */
typedef struct pts_reaction {
  struct pts_reaction *next;
  pts_promise *derived;
  pts_iterator *awaiting;
  pts_promise *adopter;
  pts_value handlers[2]; /* on fulfilled, on rejected: a function, or undefined to pass the outcome on */
  int32_t state;
  pts_value argument;
  pts_promise *follower; /* a job that makes follower take on thenable's outcome through then */
  pts_value thenable, then;
  pts_value callback; /* queueMicrotask's job; absent (zero) for the others */
  pts_iterator *streaming; /* an async generator suspended at an await */
  pts_promise *wrapping;   /* an async-from-sync iterator's next(): the promise of the result for the awaited value */
  bool wrapped_done;
  pts_iterator *returning; /* an async generator that is completed, awaiting return(value)'s value */
  pts_promise *task;       /* the promise of a task (`parallel e`) whose value this awaits */
  uint16_t owner;          /* the loop it runs on: the one that added it to its promise */
} reaction;

/* Work for another loop (D-121): settling or resolving a promise of its own, adding a reaction to one, running a reaction of its own, starting a task, or calling a runtime function there (D-137). */
enum { SETTLE, RESOLVE, THEN, REACTION, TASK, CALL };

typedef struct message {
  pts_message header;
  int32_t kind;
  int32_t state; /* SETTLE's */
  pts_promise *promise;
  reaction *reaction;
  pts_value value; /* SETTLE's result, RESOLVE's resolution, TASK's closure */
  void (*call)(void *data); /* CALL's function and its argument */
  void *data;
} message;

typedef struct rejection {
  struct rejection *next;
  pts_promise *promise;
} rejection;

/* A thread's microtasks: the queue, and the promises rejected without a handler, in order. */
typedef struct microtasks {
  reaction *first, *last;
  rejection *rejections, *last_rejection;
} microtasks;

static _Thread_local microtasks *queue; /* uncollectable, so the collector scans what it holds */

static microtasks *current(void) {
  if (queue == NULL) {
    queue = GC_MALLOC_UNCOLLECTABLE(sizeof *queue);
    if (queue == NULL) {
      pts_panic("RangeError", "Out of memory allocating the microtask queue");
    }
  }
  return queue;
}

static void *allocate(size_t size) {
  void *memory = GC_MALLOC(size);
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory allocating a promise");
  }
  return memory;
}

/* Hands loop a message of the given kind. */
static void post(uint16_t loop, int32_t kind, pts_promise *promise, reaction *r, int32_t state, pts_value value) {
  message *m = allocate(sizeof *m);
  m->header.task = kind == TASK;
  m->kind = kind;
  m->state = state;
  m->promise = promise;
  m->reaction = r;
  m->value = value;
  pts_post(loop, &m->header);
}

static void enqueue(reaction *job) {
  microtasks *q = current();
  job->next = NULL;
  pts_publish(); /* the job and what made it, before the queue holds it (D-226) */
  if (q->last != NULL) {
    q->last->next = job;
  } else {
    q->first = job;
  }
  q->last = job;
}

static pts_promise *new_promise(void) {
  pts_promise *promise = allocate(sizeof *promise);
  promise->type = &pts_type_builtin_Promise;
  promise->state = PENDING;
  promise->home = pts_current_loop & ~PTS_LOOP_FOREIGN;
  promise->result = PTS_VALUE_UNDEFINED;
  pts_publish();
  return promise;
}

static bool is_promise(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type == &pts_type_builtin_Promise;
}

bool pts_value_is_promise(pts_value value) { return is_promise(value); }

static bool is_callable(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type->kind == PTS_KIND_FUNCTION;
}

/* A reaction of a promise that settled is a job on its owner's queue. */
static void enqueue_reaction(reaction *r, int32_t state, pts_value argument) {
  r->state = state;
  r->argument = argument;
  if (r->owner != pts_current_loop) {
    post(r->owner, REACTION, NULL, r, 0, PTS_VALUE_UNDEFINED);
    return;
  }
  enqueue(r);
}

/* FulfillPromise and RejectPromise: the reactions run with the outcome, and a rejection without a handler is noted. */
static void settle(pts_promise *promise, int32_t state, pts_value result) {
  if (promise->home != pts_current_loop) {
    post(promise->home, SETTLE, promise, NULL, state, result);
    return;
  }
  reaction *r = promise->first;
  promise->state = state;
  promise->result = result;
  promise->first = promise->last = NULL;
  if (state == REJECTED && !__atomic_load_n(&promise->handled, __ATOMIC_RELAXED)) {
    microtasks *q = current();
    rejection *noted = allocate(sizeof *noted);
    noted->promise = promise;
    pts_publish();
    if (q->last_rejection != NULL) {
      q->last_rejection->next = noted;
    } else {
      q->rejections = noted;
    }
    q->last_rejection = noted;
  }
  while (r != NULL) {
    reaction *next = r->next;
    enqueue_reaction(r, state, result);
    r = next;
  }
}

enum { NOT_THENABLE, THENABLE, THEN_THREW };

/*
 * Whether value is a thenable, and its then (read once: a getter runs once): a promise's
 * own (then is undefined), or an object's method or function; or THEN_THREW and what
 * reading then threw, which rejects instead of escaping (Get(resolution, "then")).
 */
static int thenable(pts_value value, pts_value *then) {
  *then = PTS_VALUE_UNDEFINED;
  if (is_promise(value)) {
    return THENABLE;
  }
  if (!pts_value_is_object(value) || pts_value_as_object(value)->type->kind != PTS_KIND_OBJECT) {
    return NOT_THENABLE;
  }
  PTS_STATIC_STRING(then_key, 1, 4, 4, "then");
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) != 0) {
    *then = pts_caught(&handler);
    return THEN_THREW;
  }
  pts_value read = pts_dynamic_get(value, pts_value_from_string(then_key));
  pts_try_leave(&handler);
  *then = read;
  return is_callable(read) ? THENABLE : NOT_THENABLE;
}

/* ResolvePromise, once its resolving functions have decided to: fulfilled with a value, or following a thenable from a job. */
static void resolve(pts_promise *promise, pts_value resolution) {
  if (promise->home != pts_current_loop) {
    post(promise->home, RESOLVE, promise, NULL, 0, resolution);
    return;
  }
  if (pts_value_is_object(resolution) && pts_value_as_object(resolution) == (const pts_object *)promise) {
    settle(promise, REJECTED, pts_value_from_object(pts_error_new(&pts_type_builtin_TypeError, pts_string_from_cstr("Chaining cycle detected for promise #<Promise>"))));
    return;
  }
  pts_value then;
  switch (thenable(resolution, &then)) {
  case NOT_THENABLE:
    settle(promise, FULFILLED, resolution);
    return;
  case THEN_THREW:
    settle(promise, REJECTED, then);
    return;
  }
  reaction *job = allocate(sizeof *job); /* NewPromiseResolveThenableJob */
  job->follower = promise;
  job->thenable = resolution;
  job->then = then;
  enqueue(job);
}

/* PerformPromiseThen on promise's loop: r runs when promise settles, or now if it has. */
static void add_reaction(pts_promise *promise, reaction *r) {
  switch (promise->state) {
  case PENDING:
    r->next = NULL;
    pts_publish(); /* the reaction and what made it (an async function's frame), before the promise holds it (D-226) */
    if (promise->last != NULL) {
      promise->last->next = r;
    } else {
      promise->first = r;
    }
    promise->last = r;
    break;
  default:
    enqueue_reaction(r, promise->state, promise->result);
    break;
  }
  __atomic_store_n(&promise->handled, true, __ATOMIC_RELAXED);
}

/* PerformPromiseThen: r runs on this loop when promise settles, or soon if it has. */
static void perform_then(pts_promise *promise, reaction *r) {
  r->owner = pts_current_loop & ~PTS_LOOP_FOREIGN;
  if (promise->home != pts_current_loop) {
    /*
     * Handled from now on, not from when its loop takes the message: if that loop rejects
     * it meanwhile, its check for unhandled rejections (sequentially consistent with this
     * store) sees the handler this call adds, as a single loop would (D-143).
     */
    __atomic_store_n(&promise->handled, true, __ATOMIC_SEQ_CST);
    post(promise->home, THEN, promise, r, 0, PTS_VALUE_UNDEFINED);
    return;
  }
  add_reaction(promise, r);
}

/* PromiseResolve(%Promise%, value): a promise itself, or a new one resolved with value. */
static pts_promise *promise_resolve(pts_value value) {
  if (is_promise(value)) {
    return (pts_promise *)pts_value_as_object(value);
  }
  pts_promise *promise = new_promise();
  resolve(promise, value);
  pts_publish();
  return promise;
}

/* ---------------------------------------------------------------- functions the algorithms make */

/* The record a pair of resolving functions shares: the promise, and whether either has run. */
typedef struct resolution {
  pts_promise *promise;
  atomic_bool done; /* loops may race to call them */
} resolution;

typedef struct resolving_function {
  pts_closure closure;
  resolution *record;
} resolving_function;

static pts_value resolve_call(pts_closure *self, uint32_t argc, const pts_value *argv) {
  resolution *record = ((resolving_function *)self)->record;
  if (!atomic_exchange(&record->done, true)) {
    resolve(record->promise, argc > 0 ? argv[0] : PTS_VALUE_UNDEFINED);
  }
  return PTS_VALUE_UNDEFINED;
}

static pts_value reject_call(pts_closure *self, uint32_t argc, const pts_value *argv) {
  resolution *record = ((resolving_function *)self)->record;
  if (!atomic_exchange(&record->done, true)) {
    settle(record->promise, REJECTED, argc > 0 ? argv[0] : PTS_VALUE_UNDEFINED);
  }
  return PTS_VALUE_UNDEFINED;
}

static const pts_type resolve_function_type = {
    .kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(resolving_function), .length = 1, .call = resolve_call};
static const pts_type reject_function_type = {
    .kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(resolving_function), .length = 1, .call = reject_call};

/* A new runtime closure of type with room for size bytes in all, whose captures follow its header. */
static void *new_function(const pts_type *type) {
  pts_closure *closure = allocate(type->size);
  closure->type = type;
  return closure;
}

/* CreateResolvingFunctions: resolve and reject functions for promise, sharing one record. */
static void resolving_functions(pts_promise *promise, pts_value functions[2]) {
  resolution *record = allocate(sizeof *record);
  record->promise = promise;
  resolving_function *resolve_function = new_function(&resolve_function_type);
  resolving_function *reject_function = new_function(&reject_function_type);
  resolve_function->record = reject_function->record = record;
  pts_publish();
  functions[0] = pts_value_from_object(resolve_function);
  functions[1] = pts_value_from_object(reject_function);
}

/* Calls function(argv…) as a handler: false and its result, or true and what it threw. */
static bool call_catching(pts_value function, uint32_t argc, const pts_value *argv, pts_value *outcome) {
  pts_handler handler;
  volatile pts_value result = PTS_VALUE_UNDEFINED;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    PTS_STATIC_STRING(text, 1, 7, 7, "handler");
    result = pts_dynamic_call(function, text, argc, argv);
    pts_try_leave(&handler);
    *outcome = result;
    return false;
  }
  *outcome = pts_caught(&handler);
  return true;
}

/* finally's handlers: call on_finally, then pass the value on (or throw the reason) once what it returned settles. */
typedef struct finally_function {
  pts_closure closure;
  pts_value on_finally;
  pts_value outcome; /* the value or reason a thunk passes on */
} finally_function;

static pts_value value_thunk_call(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  return ((finally_function *)self)->outcome;
}

static pts_value thrower_call(pts_closure *self, uint32_t argc, const pts_value *argv) {
  (void)argc, (void)argv;
  pts_throw(((finally_function *)self)->outcome);
}

static const pts_type value_thunk_type = {
    .kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(finally_function), .call = value_thunk_call};
static const pts_type thrower_type = {
    .kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(finally_function), .call = thrower_call};

static pts_value then_with(pts_promise *promise, pts_value on_fulfilled, pts_value on_rejected);

/* thenFinally and catchFinally: on_finally(), then a promise of the outcome after what it returned. */
static pts_value finally_step(pts_closure *self, uint32_t argc, const pts_value *argv, const pts_type *thunk_type) {
  PTS_STATIC_STRING(text, 1, 9, 9, "onFinally");
  finally_function *step = (finally_function *)self;
  pts_value result = pts_dynamic_call(step->on_finally, text, 0, NULL);
  pts_promise *settled = promise_resolve(result);
  finally_function *thunk = new_function(thunk_type);
  thunk->outcome = argc > 0 ? argv[0] : PTS_VALUE_UNDEFINED;
  return then_with(settled, pts_value_from_object(thunk), PTS_VALUE_UNDEFINED);
}

static pts_value then_finally_call(pts_closure *self, uint32_t argc, const pts_value *argv) {
  return finally_step(self, argc, argv, &value_thunk_type);
}

static pts_value catch_finally_call(pts_closure *self, uint32_t argc, const pts_value *argv) {
  return finally_step(self, argc, argv, &thrower_type);
}

static const pts_type then_finally_type = {
    .kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(finally_function), .length = 1, .call = then_finally_call};
static const pts_type catch_finally_type = {
    .kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(finally_function), .length = 1, .call = catch_finally_call};

/* ---------------------------------------------------------------- jobs */

static void async_resume(pts_iterator *it, pts_resume_mode mode, pts_value sent);
static bool async_step(pts_iterator *it, pts_resume_mode mode, pts_value sent);
static void async_suspended(pts_iterator *it, bool done);

/* The async function whose body a job is running: a throw out of it reaches pts_run_microtasks' handler, which rejects its promise. */
static _Thread_local pts_iterator *resuming;

static void async_generator_step(pts_iterator *g, pts_resume_mode mode, pts_value sent);
static void async_generator_returned(pts_iterator *g, int32_t state, pts_value argument);

/* NewPromiseReactionJob's job. */
static void run_reaction(reaction *r) {
  int32_t state = r->state;
  pts_value argument = r->argument;
  if (r->task != NULL) {
    pts_task_finished();
    if (state == FULFILLED) {
      resolve(r->task, argument);
    } else {
      settle(r->task, REJECTED, argument);
    }
    return;
  }
  if (r->streaming != NULL) {
    async_generator_step(r->streaming, state == FULFILLED ? PTS_RESUME_NEXT : PTS_RESUME_THROW, argument);
    return;
  }
  if (r->returning != NULL) {
    async_generator_returned(r->returning, state, argument);
    return;
  }
  if (r->wrapping != NULL) {
    if (state == FULFILLED) {
      resolve(r->wrapping, pts_iterator_result_new(argument, r->wrapped_done));
    } else {
      settle(r->wrapping, REJECTED, argument);
    }
    return;
  }
  if (r->awaiting != NULL) {
    resuming = r->awaiting;
    bool done = async_step(r->awaiting, state == FULFILLED ? PTS_RESUME_NEXT : PTS_RESUME_THROW, argument);
    resuming = NULL;
    async_suspended(r->awaiting, done);
    return;
  }
  if (r->adopter != NULL) {
    if (state == FULFILLED) {
      resolve(r->adopter, argument);
    } else {
      settle(r->adopter, REJECTED, argument);
    }
    return;
  }
  pts_value handler = r->handlers[state == FULFILLED ? 0 : 1];
  pts_value outcome = argument;
  bool threw = state == REJECTED;
  if (handler != PTS_VALUE_UNDEFINED) {
    threw = call_catching(handler, 1, &argument, &outcome);
  }
  if (r->derived == NULL) {
    return;
  }
  if (threw) {
    settle(r->derived, REJECTED, outcome);
  } else {
    resolve(r->derived, outcome);
  }
}

/* NewPromiseResolveThenableJob's job: follower takes on the thenable's outcome. */
static void run_thenable(reaction *job) {
  if (is_promise(job->thenable)) {
    reaction *r = allocate(sizeof *r); /* then(resolve, reject) on a promise: reactions that settle the follower */
    r->adopter = job->follower;
    perform_then((pts_promise *)pts_value_as_object(job->thenable), r);
    return;
  }
  pts_value functions[2];
  resolving_functions(job->follower, functions);
  pts_value thrown; /* the then that was read, not read again: methods read through any are bound to their object (D-075) */
  if (call_catching(job->then, 2, functions, &thrown)) {
    reject_call((pts_closure *)pts_value_as_object(functions[1]), 1, &thrown);
  }
}

/* An unhandled rejection ends the program as an uncaught exception does, after the microtasks that might have handled it ran. */
static void check_rejections(microtasks *q) {
  for (rejection *noted = q->rejections; noted != NULL; noted = noted->next) {
    if (!__atomic_load_n(&noted->promise->handled, __ATOMIC_SEQ_CST)) {
      pts_report_uncaught(noted->promise->result);
    }
  }
  q->rejections = q->last_rejection = NULL;
}

/* rejected settles an async function's promise with what its body threw. */
static void rejected(pts_iterator *it, pts_value exception) {
  it->state = PTS_GENERATOR_DONE;
  settle(it->promise, REJECTED, exception);
}

void pts_run_microtasks(void) {
  microtasks *q = current();
  pts_handler handler; /* one for the bodies of all the async functions jobs resume */
  for (;;) {
    pts_try_enter(&handler);
    if (_setjmp(handler.jump) != 0) {
      pts_iterator *thrower = resuming;
      resuming = NULL;
      if (thrower == NULL) {
        pts_report_uncaught(pts_caught(&handler)); /* not an async function's: a callback of queueMicrotask's, say */
      }
      rejected(thrower, pts_caught(&handler));
      continue;
    }
    while (q->first != NULL) {
      reaction *job = q->first;
      q->first = job->next;
      if (q->first == NULL) {
        q->last = NULL;
      }
      if (job->follower != NULL) {
        run_thenable(job);
      } else if (job->callback != PTS_VALUE_ABSENT) {
        PTS_STATIC_STRING(text, 1, 8, 8, "callback");
        pts_dynamic_call(job->callback, text, 0, NULL);
      } else {
        run_reaction(job);
      }
    }
    pts_try_leave(&handler);
    break;
  }
  check_rejections(q);
}

/* ---------------------------------------------------------------- async functions */

/* Runs an async function's body from where it is suspended until it awaits (false) or ends (true). */
static bool async_step(pts_iterator *it, pts_resume_mode mode, pts_value sent) {
  it->mode = mode;
  it->sent = sent;
  return it->step(it);
}

/* After its body stopped: an async function's promise settles with what it returned, or it awaits what it yielded. */
static void async_suspended(pts_iterator *it, bool done) {
  if (done) {
    resolve(it->promise, it->value);
    return;
  }
  if (it->reaction == NULL) { /* await: the body resumes when the awaited promise settles; it awaits one at a time */
    reaction *r = allocate(sizeof *r);
    r->awaiting = it;
    pts_publish();
    it->reaction = r;
  }
  perform_then(promise_resolve(it->value), it->reaction);
}

/* Runs an async function's body until it first stops, catching what it throws (it runs inside its caller's code). */
static void async_resume(pts_iterator *it, pts_resume_mode mode, pts_value sent) {
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) != 0) {
    rejected(it, pts_caught(&handler));
    return;
  }
  bool done = async_step(it, mode, sent);
  pts_try_leave(&handler);
  async_suspended(it, done);
}

pts_value pts_async_start(pts_iterator *it) {
  it->promise = new_promise();
  async_resume(it, PTS_RESUME_NEXT, PTS_VALUE_UNDEFINED);
  pts_publish();
  return pts_value_from_object(it->promise);
}

/* ---------------------------------------------------------------- the Promise API */

pts_value pts_promise_construct(pts_value executor, const pts_string *text) {
  if (!is_callable(executor)) {
    pts_string *shown = pts_value_to_string(executor);
    pts_throw_new(&pts_type_builtin_TypeError, "Promise resolver %.*s is not a function", (int)shown->byte_len, shown->bytes);
  }
  pts_promise *promise = new_promise();
  pts_value functions[2];
  resolving_functions(promise, functions);
  pts_value thrown;
  (void)text;
  if (call_catching(executor, 2, functions, &thrown)) {
    reject_call((pts_closure *)pts_value_as_object(functions[1]), 1, &thrown);
  }
  pts_publish();
  return pts_value_from_object(promise);
}

static pts_value then_with(pts_promise *promise, pts_value on_fulfilled, pts_value on_rejected) {
  reaction *r = allocate(sizeof *r);
  r->derived = new_promise();
  r->handlers[0] = is_callable(on_fulfilled) ? on_fulfilled : PTS_VALUE_UNDEFINED;
  r->handlers[1] = is_callable(on_rejected) ? on_rejected : PTS_VALUE_UNDEFINED;
  perform_then(promise, r);
  return pts_value_from_object(r->derived);
}

pts_value pts_promise_then(pts_value promise, pts_value on_fulfilled, pts_value on_rejected) {
  if (!is_promise(promise)) { /* a PromiseLike of the program's: its own then() */
    PTS_STATIC_STRING(then_key, 1, 4, 4, "then");
    pts_value args[] = {on_fulfilled, on_rejected};
    return pts_dynamic_call_method(promise, pts_value_from_string(then_key), then_key, 2, args);
  }
  return then_with((pts_promise *)pts_value_as_object(promise), on_fulfilled, on_rejected);
}

pts_value pts_promise_finally(pts_value promise, pts_value on_finally) {
  if (!is_callable(on_finally)) {
    return pts_promise_then(promise, on_finally, on_finally);
  }
  finally_function *then_finally = new_function(&then_finally_type);
  finally_function *catch_finally = new_function(&catch_finally_type);
  then_finally->on_finally = catch_finally->on_finally = on_finally;
  return pts_promise_then(promise, pts_value_from_object(then_finally), pts_value_from_object(catch_finally));
}

void pts_queue_microtask(pts_value callback) {
  if (!is_callable(callback)) {
    pts_string *shown = pts_value_to_string(callback);
    pts_throw_new(&pts_type_builtin_TypeError, "The \"callback\" argument must be of type function. Received %.*s", (int)shown->byte_len, shown->bytes);
  }
  reaction *job = allocate(sizeof *job);
  job->callback = callback;
  if (pts_current_loop & PTS_LOOP_FOREIGN) { /* a parallel operation's callback: the job runs on its loop (D-122) */
    post(pts_current_loop & ~PTS_LOOP_FOREIGN, REACTION, NULL, job, 0, PTS_VALUE_UNDEFINED);
    return;
  }
  enqueue(job);
}

pts_value pts_promise_resolved(pts_value value) { return pts_value_from_object(promise_resolve(value)); }

pts_value pts_promise_pending(void) { return pts_value_from_object(new_promise()); }

void pts_promise_settle(pts_value promise, bool fulfilled, pts_value result) {
  pts_promise *settled = (pts_promise *)pts_value_as_object(promise);
  if (fulfilled) {
    resolve(settled, result);
  } else {
    settle(settled, REJECTED, result);
  }
}

pts_value pts_promise_rejected(pts_value reason) {
  pts_promise *promise = new_promise();
  settle(promise, REJECTED, reason);
  pts_publish();
  return pts_value_from_object(promise);
}

/* ---------------------------------------------------------------- tasks and other loops' messages (D-121) */

/* Starts a task on this loop: its closure's value settles the task's promise, the value's outcome if it is a promise. */
static void run_task(pts_value task, pts_promise *promise) {
  pts_closure *closure = (pts_closure *)pts_value_as_object(task);
  pts_handler handler;
  pts_try_enter(&handler);
  pts_parallel_nesting++;
  if (_setjmp(handler.jump) != 0) {
    pts_parallel_nesting--;
    pts_task_finished();
    settle(promise, REJECTED, pts_caught(&handler));
    return;
  }
  pts_value value = ((pts_value(*)(pts_closure *))closure->code)(closure);
  pts_try_leave(&handler);
  pts_parallel_nesting--;
  reaction *done = allocate(sizeof *done);
  done->task = promise;
  perform_then(promise_resolve(value), done);
}

void pts_message_run(pts_message *header) {
  pts_message_apply(header);
  pts_run_microtasks();
}

void pts_message_apply(pts_message *header) {
  message *m = (message *)header;
  switch (m->kind) {
  case SETTLE:
    settle(m->promise, m->state, m->value);
    break;
  case RESOLVE:
    resolve(m->promise, m->value);
    break;
  case THEN:
    add_reaction(m->promise, m->reaction);
    break;
  case REACTION:
    enqueue(m->reaction);
    break;
  case TASK:
    run_task(m->value, m->promise);
    break;
  case CALL:
    m->call(m->data);
    break;
  }
}

void pts_post_call(uint16_t loop, void (*call)(void *data), void *data) {
  message *m = allocate(sizeof *m);
  m->kind = CALL;
  m->call = call;
  m->data = data;
  pts_post(loop, &m->header);
}

pts_value pts_parallel(pts_value task) {
  pts_promise *promise = new_promise();
  post(pts_place_task(), TASK, promise, NULL, 0, task);
  return pts_value_from_object(promise);
}

/* ---------------------------------------------------------------- combinators */

/* A plain object of the given properties, in order, as JS's object literals make (hash-map objects of any, as JSON.parse's). */
static pts_value record_of(const char *const keys[], const pts_value values[], size_t count) {
  pts_map *record = pts_map_new(&pts_type_any_record);
  for (size_t i = 0; i < count; i++) {
    pts_string *key = pts_string_from_cstr(keys[i]);
    pts_map_set(record, &key, &values[i]);
  }
  pts_publish();
  return pts_value_from_object(record);
}

enum { ALL, ALL_SETTLED, ANY };

/* What Promise.all, allSettled and any keep across their items: the promise they return, the values (or reasons) so far, how many are outstanding. */
typedef struct combination {
  pts_promise *promise;
  pts_value reject; /* the promise's reject function, for all */
  pts_array *values;
  size_t remaining;
  int kind;
} combination;

/* The function that records an item's outcome at index; allSettled's two share called. */
typedef struct element_function {
  pts_closure closure;
  combination *state;
  size_t index;
  bool *called;
} element_function;

/* The last outstanding item settles the combination: all and allSettled fulfil it with the values, any rejects it. */
static void element_done(combination *state) {
  if (--state->remaining > 0) {
    return;
  }
  pts_value values = pts_value_from_object(state->values);
  if (state->kind == ANY) {
    settle(state->promise, REJECTED, pts_aggregate_error_new(values, pts_string_from_cstr("All promises were rejected")));
  } else {
    resolve(state->promise, values);
  }
}

static pts_value element_call(pts_closure *self, uint32_t argc, const pts_value *argv, bool fulfilled) {
  element_function *element = (element_function *)self;
  if (*element->called) {
    return PTS_VALUE_UNDEFINED;
  }
  *element->called = true;
  pts_value outcome = argc > 0 ? argv[0] : PTS_VALUE_UNDEFINED;
  if (element->state->kind == ALL_SETTLED) {
    static const char *const fulfilled_keys[] = {"status", "value"};
    static const char *const rejected_keys[] = {"status", "reason"};
    pts_value parts[] = {pts_value_from_string(pts_string_from_cstr(fulfilled ? "fulfilled" : "rejected")), outcome};
    outcome = record_of(fulfilled ? fulfilled_keys : rejected_keys, parts, 2);
  }
  pts_array_set(element->state->values, (double)element->index, &outcome);
  element_done(element->state);
  return PTS_VALUE_UNDEFINED;
}

static pts_value resolve_element_call(pts_closure *self, uint32_t argc, const pts_value *argv) { return element_call(self, argc, argv, true); }
static pts_value reject_element_call(pts_closure *self, uint32_t argc, const pts_value *argv) { return element_call(self, argc, argv, false); }

static const pts_type resolve_element_type = {
    .kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(element_function), .length = 1, .call = resolve_element_call};
static const pts_type reject_element_type = {
    .kind = PTS_KIND_FUNCTION, .gc_descr = PTS_GC_NO_POINTERS, .name = "", .size = sizeof(element_function), .length = 1, .call = reject_element_call};

static pts_value element_function_new(const pts_type *type, combination *state, size_t index, bool *called) {
  element_function *element = new_function(type);
  element->state = state;
  element->index = index;
  element->called = called;
  pts_publish();
  return pts_value_from_object(element);
}

/*
 * PerformPromiseAll, PerformPromiseAllSettled, PerformPromiseAny and PerformPromiseRace
 * over the items of iterable: Promise.resolve of each, then then() with the functions
 * that record its outcome (race: the combination's own resolving functions).
 */
static void perform_combination(int kind, bool race, pts_value iterable, const pts_string *text, pts_promise *promise, const pts_value functions[2]) {
  combination *state = allocate(sizeof *state);
  state->promise = promise;
  state->reject = functions[1];
  state->values = pts_array_of(&pts_array_types[PTS_FIELD_VALUE], NULL, 0);
  state->remaining = 1;
  state->kind = kind;
  pts_value iterator = pts_iterator_open(iterable, text);
  for (size_t index = 0; !pts_iterator_resume(iterator, PTS_RESUME_NEXT, PTS_VALUE_UNDEFINED); index++) {
    pts_promise *item = promise_resolve(pts_iterator_value(iterator));
    if (race) {
      then_with(item, functions[0], functions[1]);
      continue;
    }
    pts_value undefined = PTS_VALUE_UNDEFINED;
    pts_array_push(state->values, &undefined, 1);
    bool *called = allocate(sizeof *called);
    state->remaining++;
    switch (kind) {
    case ALL:
      then_with(item, element_function_new(&resolve_element_type, state, index, called), functions[1]);
      break;
    case ALL_SETTLED:
      then_with(item, element_function_new(&resolve_element_type, state, index, called), element_function_new(&reject_element_type, state, index, called));
      break;
    default:
      then_with(item, functions[0], element_function_new(&reject_element_type, state, index, called));
      break;
    }
  }
  if (!race) {
    element_done(state);
  }
}

static pts_value combine(int kind, bool race, pts_value iterable, const pts_string *text) {
  pts_promise *promise = new_promise();
  pts_value functions[2];
  resolving_functions(promise, functions);
  pts_handler handler; /* what the iteration throws rejects the promise */
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) == 0) {
    perform_combination(kind, race, iterable, text, promise, functions);
    pts_try_leave(&handler);
  } else {
    pts_value thrown = pts_caught(&handler);
    reject_call((pts_closure *)pts_value_as_object(functions[1]), 1, &thrown);
  }
  pts_publish();
  return pts_value_from_object(promise);
}

pts_value pts_promise_all(pts_value iterable, const pts_string *text) { return combine(ALL, false, iterable, text); }
pts_value pts_promise_all_settled(pts_value iterable, const pts_string *text) { return combine(ALL_SETTLED, false, iterable, text); }
pts_value pts_promise_any(pts_value iterable, const pts_string *text) { return combine(ANY, false, iterable, text); }
pts_value pts_promise_race(pts_value iterable, const pts_string *text) { return combine(ALL, true, iterable, text); }

pts_value pts_promise_with_resolvers(void) {
  pts_promise *promise = new_promise();
  pts_value parts[3];
  parts[0] = pts_value_from_object(promise);
  resolving_functions(promise, &parts[1]);
  static const char *const keys[] = {"promise", "resolve", "reject"};
  return record_of(keys, parts, 3);
}

void pts_check_top_level(pts_value promise) {
  if (((pts_promise *)pts_value_as_object(promise))->state == PENDING) {
    pts_begin_exit();
    pts_flush_output(stdout);
    fputs("Warning: Detected unsettled top-level await\n", stderr);
    exit(13);
  }
}

/* ---------------------------------------------------------------- async generators (D-115) */

typedef struct pts_request {
  struct pts_request *next;
  pts_iterator *generator;
  pts_resume_mode mode;
  pts_value value;
  pts_promise *promise; /* made on the loop that asked, which awaits it */
} request;

static const pts_type async_generator_type = {
    .kind = PTS_KIND_GENERATOR,
    .gc_descr = PTS_GC_NO_POINTERS, /* frames are allocated with GC_MALLOC and scanned whole */
    .name = "AsyncGenerator",
    .size = sizeof(pts_iterator),
};

bool pts_value_is_async_generator(pts_value value) {
  return pts_value_is_object(value) && pts_value_as_object(value)->type == &async_generator_type;
}

pts_value pts_iterator_result_new(pts_value value, bool done) {
  static const char *const keys[] = {"value", "done"};
  pts_value parts[] = {value, pts_value_from_bool(done)};
  return record_of(keys, parts, 2);
}

pts_value pts_async_generator_new(pts_iterator *it) {
  it->type = &async_generator_type;
  it->home = pts_current_loop & ~PTS_LOOP_FOREIGN;
  pts_publish();
  return pts_value_from_object(it);
}

/* AsyncGeneratorCompleteStep: the first request's promise settles, and it leaves the queue. */
static void complete_request(pts_iterator *g, bool rejected, pts_value value, bool done) {
  request *r = g->requests;
  g->requests = r->next;
  if (rejected) {
    settle(r->promise, REJECTED, value);
  } else {
    settle(r->promise, FULFILLED, pts_iterator_result_new(value, done));
  }
}

static void async_generator_drain(pts_iterator *g);

/* After return(value) on a completed generator: its awaited value completes the request. */
static void async_generator_returned(pts_iterator *g, int32_t state, pts_value argument) {
  g->executing = false;
  complete_request(g, state == REJECTED, argument, true);
  async_generator_drain(g);
}

/* Runs the body from where it stopped until it awaits (which resumes it from a job), yields or ends. */
static void async_generator_step(pts_iterator *g, pts_resume_mode mode, pts_value sent) {
  g->mode = mode;
  g->sent = sent;
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) != 0) {
    g->state = PTS_GENERATOR_DONE;
    g->executing = false;
    complete_request(g, true, pts_caught(&handler), true);
    async_generator_drain(g);
    return;
  }
  bool done = g->step(g);
  pts_try_leave(&handler);
  if (!done && g->awaiting) {
    reaction *r = allocate(sizeof *r);
    r->streaming = g;
    perform_then(promise_resolve(g->value), r);
    return;
  }
  g->executing = false;
  complete_request(g, false, g->value, done);
  async_generator_drain(g);
}

/* AsyncGeneratorEnqueue on the generator's loop: the request joins its queue, which is served if the body isn't running. */
static void queue_request(request *r) {
  request **last = &r->generator->requests;
  while (*last != NULL) {
    last = &(*last)->next;
  }
  pts_publish();
  *last = r;
  async_generator_drain(r->generator);
}

static void queue_request_call(void *r) { queue_request(r); }

/* AsyncGeneratorDrainQueue and AsyncGeneratorResumeNext: serves the queued requests while the body isn't running. */
static void async_generator_drain(pts_iterator *g) {
  while (g->requests != NULL && !g->executing) {
    request *r = g->requests;
    if (g->state == PTS_GENERATOR_START && r->mode != PTS_RESUME_NEXT) {
      g->state = PTS_GENERATOR_DONE;
    }
    if (g->state != PTS_GENERATOR_DONE) {
      g->executing = true;
      async_generator_step(g, r->mode, r->value);
      return;
    }
    switch (r->mode) {
    case PTS_RESUME_NEXT:
      complete_request(g, false, PTS_VALUE_UNDEFINED, true);
      break;
    case PTS_RESUME_THROW:
      complete_request(g, true, r->value, true);
      break;
    case PTS_RESUME_RETURN: { /* awaiting-return: the value is awaited first */
      g->executing = true;
      reaction *returned = allocate(sizeof *returned);
      returned->returning = g;
      perform_then(promise_resolve(r->value), returned);
      return;
    }
    }
  }
}

/* An async-from-sync iterator (CreateAsyncFromSyncIterator): a sync iterator whose items it gives as promises. */
typedef struct from_sync {
  pts_iterator header;
  pts_value iterator;
} from_sync;

static const pts_type from_sync_type = {
    .kind = PTS_KIND_GENERATOR,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "Async-from-Sync Iterator",
    .size = sizeof(from_sync),
};

static pts_value from_sync_resume(from_sync *it, pts_resume_mode mode, pts_value sent) {
  pts_promise *promise = new_promise();
  pts_handler handler; /* what the sync iterator throws rejects the promise */
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) != 0) {
    settle(promise, REJECTED, pts_caught(&handler));
    pts_publish();
    return pts_value_from_object(promise);
  }
  bool done = pts_iterator_resume(it->iterator, mode, sent);
  pts_value value = pts_iterator_value(it->iterator);
  pts_try_leave(&handler);
  reaction *r = allocate(sizeof *r); /* AsyncFromSyncIteratorContinuation: the value awaited, then the result */
  r->wrapping = promise;
  r->wrapped_done = done;
  perform_then(promise_resolve(value), r);
  pts_publish();
  return pts_value_from_object(promise);
}

pts_value pts_async_iterator_open(pts_value iterable, const pts_string *text) {
  if (pts_value_is_async_generator(iterable)) {
    return iterable;
  }
  PTS_STATIC_STRING(async_iterator_key, 1, 15, 15, "@@asyncIterator");
  if (pts_value_is_object(iterable) && pts_value_as_object(iterable)->type->kind == PTS_KIND_OBJECT) {
    pts_value method = pts_dynamic_get(iterable, pts_value_from_string(async_iterator_key));
    if (is_callable(method)) {
      return pts_dynamic_call(method, text, 0, NULL); /* the function read, which a getter gave once (D-179) */
    }
  }
  from_sync *it = allocate(sizeof *it);
  it->header.type = &from_sync_type;
  it->iterator = pts_iterator_open(iterable, text);
  pts_publish();
  return pts_value_from_object(it);
}

pts_value pts_async_iterator_resume(pts_value iterator, pts_resume_mode mode, pts_value sent) {
  if (pts_value_is_async_generator(iterator)) {
    request *r = allocate(sizeof *r);
    r->generator = (pts_iterator *)pts_value_as_object(iterator);
    r->mode = mode;
    r->value = sent;
    r->promise = new_promise();
    if (r->generator->home != pts_current_loop) { /* its queue and body belong to its loop, which takes the request in order (D-143) */
      pts_post_call(r->generator->home, queue_request_call, r);
    } else {
      queue_request(r);
    }
    return pts_value_from_object(r->promise);
  }
  if (pts_value_is_object(iterator) && pts_value_as_object(iterator)->type == &from_sync_type) {
    return from_sync_resume((from_sync *)pts_value_as_object(iterator), mode, sent);
  }
  static const char *const names[] = {"next", "throw", "return"};
  pts_string *name = pts_string_from_cstr(names[mode]);
  return pts_dynamic_call_method(iterator, pts_value_from_string(name), name, 1, &sent);
}

pts_value pts_async_iterator_close(pts_value iterator) {
  if (pts_value_is_async_generator(iterator)) {
    return pts_async_iterator_resume(iterator, PTS_RESUME_RETURN, PTS_VALUE_UNDEFINED);
  }
  if (pts_value_is_object(iterator) && pts_value_as_object(iterator)->type == &from_sync_type) {
    pts_iterator_close(((from_sync *)pts_value_as_object(iterator))->iterator);
    return PTS_VALUE_UNDEFINED;
  }
  PTS_STATIC_STRING(return_key, 1, 6, 6, "return");
  if (!pts_value_is_object(iterator) || !is_callable(pts_dynamic_get(iterator, pts_value_from_string(return_key)))) {
    return PTS_VALUE_UNDEFINED;
  }
  return pts_dynamic_call_method(iterator, pts_value_from_string(return_key), return_key, 0, NULL);
}

bool pts_iterator_result_done(pts_value result) {
  if (!pts_value_is_object(result)) {
    pts_string *shown = pts_value_to_string(result);
    pts_throw_new(&pts_type_builtin_TypeError, "Iterator result %.*s is not an object", (int)shown->byte_len, shown->bytes);
  }
  PTS_STATIC_STRING(done_key, 1, 4, 4, "done");
  return pts_value_truthy(pts_dynamic_get(result, pts_value_from_string(done_key)));
}

pts_value pts_iterator_result_value(pts_value result) {
  PTS_STATIC_STRING(value_key, 1, 5, 5, "value");
  return pts_dynamic_get(result, pts_value_from_string(value_key));
}

int32_t pts_promise_state(const pts_object *promise, pts_value *result) {
  const pts_promise *p = (const pts_promise *)promise;
  *result = p->result;
  return p->state;
}
