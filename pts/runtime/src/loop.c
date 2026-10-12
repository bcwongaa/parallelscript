#include "pts.h"

#include <stdlib.h>
#include <uv.h>

/*
 * The event loop's timers (D-114) on the thread's libuv loop (scheduler.c, D-121), as
 * Node's. setTimeout and setInterval timers wait in a heap ordered by when they are due,
 * then by when they were scheduled, behind one uv timer armed for the first;
 * setImmediate's callbacks wait in a queue that a uv check handle runs after the loop
 * polls. Microtasks run after each callback, as in Node.
 */

struct pts_timer {
  const pts_type *type;
  pts_value callback;
  pts_array *args; /* the arguments to call it with, or NULL */
  uint64_t delay;  /* in ms, at least 1 */
  uint64_t due;    /* in the loop's ms */
  uint64_t order;  /* when it was scheduled, among timers due at the same time */
  size_t slot;     /* its index in the heap while it is there */
  bool repeat;     /* setInterval's */
  bool active;     /* in the heap, or running */
  bool referenced; /* keeps the loop alive (ref, unref) */
};

const pts_type pts_type_builtin_Timeout = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_NO_POINTERS, /* allocated with GC_MALLOC and scanned whole */
    .name = "Timeout",
    .size = sizeof(pts_timer),
};

typedef struct immediate {
  const pts_type *type;
  struct immediate *next;
  pts_value callback;
  pts_array *args;
  bool active;
} immediate;

static const pts_type immediate_type = {
    .kind = PTS_KIND_OBJECT,
    .gc_descr = PTS_GC_NO_POINTERS,
    .name = "Immediate",
    .size = sizeof(immediate),
};

/* A thread's timers: libuv's handles (malloc'd, holding no collected pointers) and ours (uncollectable, so the collector scans the heap and queue it holds). */
typedef struct loop_state {
  uv_loop_t *loop;
  uv_timer_t *timer;
  uv_check_t *check;
  uv_idle_t *idle; /* keeps the loop from blocking in poll while immediates wait */
  pts_timer **heap;
  size_t count, capacity;
  uint64_t order;
  size_t referenced; /* active timers that keep the loop alive */
  immediate *first, *last;
} loop_state;

static _Thread_local loop_state *state;

static void *checked(void *memory) {
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory in the event loop");
  }
  return memory;
}

static void on_timer(uv_timer_t *handle);
static void on_check(uv_check_t *handle);

void pts_loop_init(void) {
  if (state != NULL) {
    return;
  }
  loop_state *s = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *s));
  s->loop = pts_uv_loop();
  s->timer = checked(malloc(sizeof *s->timer));
  s->check = checked(malloc(sizeof *s->check));
  s->idle = checked(malloc(sizeof *s->idle));
  uv_timer_init(s->loop, s->timer);
  uv_check_init(s->loop, s->check);
  uv_idle_init(s->loop, s->idle);
  s->timer->data = s->check->data = s;
  state = s;
}

/* ---------------------------------------------------------------- the heap of timers */

static bool earlier(const pts_timer *a, const pts_timer *b) { return a->due < b->due || (a->due == b->due && a->order < b->order); }

static void place(loop_state *s, size_t slot, pts_timer *timer) {
  s->heap[slot] = timer;
  timer->slot = slot;
}

static void sift_up(loop_state *s, size_t slot) {
  pts_timer *timer = s->heap[slot];
  while (slot > 0 && earlier(timer, s->heap[(slot - 1) / 2])) {
    place(s, slot, s->heap[(slot - 1) / 2]);
    slot = (slot - 1) / 2;
  }
  place(s, slot, timer);
}

static void sift_down(loop_state *s, size_t slot) {
  pts_timer *timer = s->heap[slot];
  for (;;) {
    size_t child = 2 * slot + 1;
    if (child >= s->count) {
      break;
    }
    if (child + 1 < s->count && earlier(s->heap[child + 1], s->heap[child])) {
      child++;
    }
    if (!earlier(s->heap[child], timer)) {
      break;
    }
    place(s, slot, s->heap[child]);
    slot = child;
  }
  place(s, slot, timer);
}

/* Arms the uv timer for the first timer due, and keeps the loop alive while a referenced timer waits. */
static void arm(loop_state *s) {
  if (s->count == 0) {
    uv_timer_stop(s->timer);
    return;
  }
  uint64_t now = uv_now(s->loop);
  uint64_t due = s->heap[0]->due;
  uv_timer_start(s->timer, on_timer, due > now ? due - now : 0, 0);
  if (s->referenced > 0) {
    uv_ref((uv_handle_t *)s->timer);
  } else {
    uv_unref((uv_handle_t *)s->timer);
  }
}

/* Puts a timer in the heap, due its delay after start (the loop's time when it was scheduled, as Node's getLibuvNow). */
static void schedule(loop_state *s, pts_timer *timer, uint64_t start) {
  if (s->count == s->capacity) {
    size_t capacity = s->capacity ? 2 * s->capacity : 16;
    pts_timer **heap = checked(GC_MALLOC(capacity * sizeof *heap));
    for (size_t i = 0; i < s->count; i++) {
      heap[i] = s->heap[i];
    }
    s->heap = heap;
    s->capacity = capacity;
  }
  timer->due = start + timer->delay;
  timer->order = s->order++;
  timer->active = true;
  s->referenced += timer->referenced;
  place(s, s->count++, timer);
  sift_up(s, timer->slot);
}

static void unschedule(loop_state *s, pts_timer *timer) {
  size_t slot = timer->slot;
  pts_timer *last = s->heap[--s->count];
  s->heap[s->count] = NULL;
  if (slot < s->count) {
    place(s, slot, last);
    sift_up(s, slot);
    sift_down(s, last->slot);
  }
}

/* Runs a callback with its arguments; then the microtasks it queued run, as after every callback in Node. */
static void run_callback(pts_value callback, pts_array *args) {
  PTS_STATIC_STRING(text, PTS_STRING_ASCII, 8, 8, "callback");
  size_t argc = args == NULL ? 0 : pts_array_length_of(args);
  pts_value argv[argc > 0 ? argc : 1];
  for (size_t i = 0; i < argc; i++) {
    argv[i] = pts_array_element(args, (double)i);
  }
  pts_dynamic_call(callback, text, (uint32_t)argc, argv);
  pts_run_microtasks();
}

/* Node's processTimers: every timer due by now, in order, each followed by its microtasks; an interval is due again its delay after it started. */
static void on_timer(uv_timer_t *handle) {
  loop_state *s = handle->data;
  uint64_t now = uv_now(s->loop);
  while (s->count > 0 && s->heap[0]->due <= now) {
    pts_timer *timer = s->heap[0];
    unschedule(s, timer);
    s->referenced -= timer->referenced;
    run_callback(timer->callback, timer->args);
    if (timer->active && timer->repeat) {
      schedule(s, timer, now);
    } else {
      timer->active = false;
    }
  }
  arm(s);
}

/* The delay setTimeout takes: at least 1 ms, and 1 for anything that isn't a number of ms up to 2^31 - 1, as Node's. */
static uint64_t delay_of(pts_value ms) {
  double delay = ms == PTS_VALUE_UNDEFINED ? 1 : pts_value_to_number(ms);
  if (!(delay >= 1 && delay <= 2147483647.0)) {
    return 1;
  }
  return (uint64_t)delay;
}

pts_value pts_timer_set(pts_value callback, pts_value ms, pts_array *args, bool repeat) {
  if (!pts_value_is_object(callback) || pts_value_as_object(callback)->type->kind != PTS_KIND_FUNCTION) {
    pts_string *shown = pts_value_to_string(callback);
    pts_throw_new(&pts_type_builtin_TypeError, "The \"callback\" argument must be of type function. Received %.*s", (int)shown->byte_len, shown->bytes);
  }
  pts_loop_init();
  pts_timer *timer = checked(GC_MALLOC(sizeof *timer));
  timer->type = &pts_type_builtin_Timeout;
  timer->callback = callback;
  timer->args = args != NULL && pts_array_length_of(args) > 0 ? args : NULL;
  timer->delay = delay_of(ms);
  timer->repeat = repeat;
  timer->referenced = true;
  schedule(state, timer, uv_now(state->loop));
  arm(state);
  return pts_value_from_object(timer);
}

static pts_timer *as_timer(pts_value value) {
  if (!pts_value_is_object(value) || pts_value_as_object(value)->type != &pts_type_builtin_Timeout) {
    return NULL;
  }
  return (pts_timer *)pts_value_as_object(value);
}

void pts_timer_clear(pts_value value) {
  pts_timer *timer = as_timer(value);
  if (timer == NULL || !timer->active) {
    return; /* clearTimeout of undefined, of another value, or of a timer that has fired: nothing */
  }
  timer->active = false;
  if (state->count > timer->slot && state->heap[timer->slot] == timer) { /* not running now */
    unschedule(state, timer);
    state->referenced -= timer->referenced;
    arm(state);
  }
}

pts_value pts_timer_ref(pts_value value, bool referenced) {
  pts_timer *timer = as_timer(value);
  if (timer != NULL && timer->referenced != referenced) {
    timer->referenced = referenced;
    if (timer->active && state->count > timer->slot && state->heap[timer->slot] == timer) {
      state->referenced += referenced ? 1 : (size_t)-1;
      arm(state);
    }
  }
  return value;
}

bool pts_timer_has_ref(pts_value value) {
  pts_timer *timer = as_timer(value);
  return timer != NULL && timer->referenced;
}

pts_value pts_timer_refresh(pts_value value) {
  pts_timer *timer = as_timer(value);
  if (timer != NULL && timer->active && state->count > timer->slot && state->heap[timer->slot] == timer) {
    unschedule(state, timer);
    state->referenced -= timer->referenced;
    schedule(state, timer, uv_now(state->loop));
    arm(state);
  }
  return value;
}

/* ---------------------------------------------------------------- immediates */

/* Node's processImmediate: the immediates queued before this round, each followed by its microtasks. */
static void on_check(uv_check_t *handle) {
  loop_state *s = handle->data;
  immediate *last = s->last;
  while (s->first != NULL) {
    immediate *next = s->first;
    s->first = next->next;
    if (s->first == NULL) {
      s->last = NULL;
    }
    if (next->active) {
      next->active = false;
      run_callback(next->callback, next->args);
    }
    if (next == last) {
      break;
    }
  }
  if (s->first == NULL) {
    uv_check_stop(s->check);
    uv_idle_stop(s->idle);
  }
}

static void on_idle(uv_idle_t *handle) { (void)handle; }

pts_value pts_immediate_set(pts_value callback, pts_array *args) {
  if (!pts_value_is_object(callback) || pts_value_as_object(callback)->type->kind != PTS_KIND_FUNCTION) {
    pts_string *shown = pts_value_to_string(callback);
    pts_throw_new(&pts_type_builtin_TypeError, "The \"callback\" argument must be of type function. Received %.*s", (int)shown->byte_len, shown->bytes);
  }
  pts_loop_init();
  immediate *it = checked(GC_MALLOC(sizeof *it));
  it->type = &immediate_type;
  it->callback = callback;
  it->args = args != NULL && pts_array_length_of(args) > 0 ? args : NULL;
  it->active = true;
  if (state->last != NULL) {
    state->last->next = it;
  } else {
    state->first = it;
    uv_check_start(state->check, on_check);
    uv_idle_start(state->idle, on_idle);
  }
  state->last = it;
  return pts_value_from_object(it);
}

void pts_immediate_clear(pts_value value) {
  if (pts_value_is_object(value) && pts_value_as_object(value)->type == &immediate_type) {
    ((immediate *)pts_value_as_object(value))->active = false;
  }
}

/* ---------------------------------------------------------------- performance.now() */

static uint64_t time_origin;

void pts_time_origin_init(void) { time_origin = uv_hrtime(); }

double pts_performance_now(void) { return (double)(uv_hrtime() - time_origin) / 1e6; }
