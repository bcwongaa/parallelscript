/*
 * The publication checker (H35b), a test-only dynamic check of ARCHITECTURE §4.13 rule 6
 * that runs on x86-64: a new object must be filled before any other thread can reach it.
 * x86-64 keeps stores in order by itself, aarch64 only behind a store-store barrier
 * (pts_publish), so the checker reports each place where a thread makes an object it
 * allocated reachable from memory other threads can read while words of it were written
 * since the thread's last barrier (C356).
 *
 * It exists only under -DPTS_PUBLICATION_CHECK (toolchain.PublicationCheck): clang then
 * calls __sanitizer_cov_store<N>(address) before each store the runtime and the program
 * make, atomic stores included (-fsanitize-coverage=func,trace-stores), and the linker
 * sends the runtime's allocations and pthread locks through the wraps below
 * (-Wl,--wrap=GC_malloc,...). Each thread keeps:
 *
 * - a table of its fresh objects: allocated by it and not yet reachable from shared
 *   memory, with whether each was written since the thread's last barrier (dirty);
 * - an allocation adds an object, dirty: bdwgc zeroed it and may have stored its type;
 * - a store into a fresh object is a fill, which makes it dirty: by default only a word's
 *   first store since the allocation (a word that held zero and no store had written; a
 *   later store changes a value another thread could already read whole); with
 *   PTS_PUBLICATION_CHECK=strict, every store;
 * - a barrier (pts_publication_barrier_hook) makes every fresh object clean; a release
 *   store the runtime announces (PTS_PUBLICATION_RELEASE) makes the objects it names clean;
 * - a store of a pointer to a fresh object into shared memory (static data, the collected
 *   heap outside the thread's fresh objects, another thread's stack) publishes it: if the
 *   object is dirty, that is a violation; else the object is shared from then on (stores
 *   into it are mutations, not fills), and so is every fresh object it holds a pointer to,
 *   which is a violation if dirty. Stores into the thread's stack, its static TLS block
 *   and malloc's memory (scratch copies: bdwgc doesn't scan it) publish nothing, and
 *   neither does a store made while the thread holds a pthread mutex or runs a
 *   pthread_once routine, whose readers take the same lock.
 *
 * The callbacks get the address before the store, so each store into shared memory is
 * checked at the thread's next event (store, allocation, barrier, lock, exit), by reading
 * the value it left there. A violation prints where the object was made and where it was
 * stored and ends the program with VIOLATION_EXIT. PTS_PUBLICATION_CHECK takes options,
 * separated by commas: all (report each place once and go on, exiting with
 * VIOLATION_EXIT at the end), count (report only the total), stats (print what the
 * checker saw), strict (see above), uncollectable (also track the runtime's uncollectable
 * structures, which threads reach through locks, pthread_once and thread creation),
 * malloc (count malloc's memory as shared) and partial (a release store doesn't make the
 * objects it orders clean for other paths to them; see pts_publication_check_release).
 *
 * What it doesn't see: stores clang doesn't make (memcpy, memset, inline assembly, the
 * vendored libraries; compare-and-swap and exchange unless announced with
 * PTS_PUBLICATION_STORE), objects not allocated through the wraps, the readers' side
 * (whether a reader's loads are ordered after the load that found the object), which
 * stores the program meant as changes rather than fills, and objects it has forgotten
 * (the table keeps a thread's newest RING_SIZE objects, and drops those a collection
 * didn't mark).
 */
#include "pts.h"

#ifdef PTS_PUBLICATION_CHECK

#include <backtrace.h>
#include <link.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

/* The checker's own code must not call back into itself. */
#pragma clang attribute push(__attribute__((no_sanitize("coverage"))), apply_to = function)

enum {
  RING_SIZE = 1 << 15,            /* fresh objects a thread keeps; the oldest are forgotten */
  HASH_SIZE = 2 * RING_SIZE,      /* open addressing, at most half full */
  INLINE_WORDS = 64,              /* objects up to this many words keep their written words inline */
  MAX_BITMAP_WORDS = 1 << 24,     /* larger objects keep none: a word holding zero counts as unwritten */
  SITE_FRAMES = 4,                /* return addresses kept of an allocation */
  STORE_FRAMES = 3,               /* and of a store */
  MAX_REPORTED = 512,             /* distinct places PTS_PUBLICATION_CHECK=all reports */
  SHARE_STACK = 256,              /* objects share() handles before it stops following pointers */
  VIOLATION_EXIT = 70,
};

enum { HOLDS_POINTERS = 1, UNCOLLECTABLE = 2, SLOTS = 4 };
enum { MAX_PRIVATE = 16 }; /* the thread's own records it reaches only through thread-local variables */

typedef struct fresh_object {
  uintptr_t base; /* 0 for a free entry */
  size_t size;
  uint64_t dirty_epoch; /* written in this barrier epoch of its thread: dirty */
  uint64_t written;     /* the words a store has written, for objects of up to INLINE_WORDS */
  uint64_t *written_words; /* for larger ones, or NULL */
  uint32_t flags;
  uintptr_t site[SITE_FRAMES];
} fresh_object;

typedef struct pending_store {
  uintptr_t address; /* 0: none */
  size_t size;
  uintptr_t pc[STORE_FRAMES];
} pending_store;

/* What PTS_PUBLICATION_CHECK=stats prints at exit, to show the checker saw the program. */
typedef struct checker_stats {
  size_t allocations, heap_stores, fills, publications, barriers;
} checker_stats;

typedef struct checker_thread {
  uint64_t epoch;
  uintptr_t stack_low, stack_high, tls_low, tls_high;
  uintptr_t fresh_low, fresh_high; /* every fresh object lies between them */
  GC_word gc_no;                   /* the collection the table was last checked against */
  size_t next;                     /* the ring's next entry */
  fresh_object *last;              /* the object the last lookup found */
  uintptr_t private_low[MAX_PRIVATE], private_high[MAX_PRIVATE]; /* PTS_PUBLICATION_PRIVATE's */
  size_t private_count;
  pending_store pending;
  checker_stats stats;
  struct checker_thread *next_thread; /* in the list of live threads, under report_lock */
  fresh_object ring[RING_SIZE];
  uint32_t hash[HASH_SIZE]; /* ring index + 1 by base, 0 for none */
} checker_thread;

static __thread __attribute__((tls_model("initial-exec"))) int busy; /* in the checker, or retired */
static __thread __attribute__((tls_model("initial-exec"))) checker_thread *self;
/* The pthread mutexes this thread holds and the pthread_once routines it is in. */
static __thread __attribute__((tls_model("initial-exec"))) int locks_held;

static pthread_once_t init_once = PTHREAD_ONCE_INIT;
static pthread_key_t exit_key;
static bool strict_mode, report_all, report_none, report_partial, track_uncollectable, check_malloc, print_stats;
static checker_stats totals;           /* of threads that ended, under report_lock */
static checker_thread *live_threads;    /* under report_lock */
static uintptr_t executable_low, executable_high;

static pthread_mutex_t report_lock = PTHREAD_MUTEX_INITIALIZER;
static struct backtrace_state *symbols;
static size_t violations;
static size_t reported_count;
static uintptr_t reported[MAX_REPORTED][2]; /* store pc, allocation site */

static void finish_pending(checker_thread *t);
static void report_violation(checker_thread *t, const fresh_object *object, uintptr_t address, const uintptr_t *store_pcs, const fresh_object *via);

/* ---------------------------------------------------------------- setup */

static int find_executable(struct dl_phdr_info *info, size_t size, void *data) {
  (void)size;
  (void)data;
  uintptr_t low = UINTPTR_MAX, high = 0;
  for (int i = 0; i < info->dlpi_phnum; i++) {
    const ElfW(Phdr) *ph = &info->dlpi_phdr[i];
    if (ph->p_type == PT_LOAD) {
      uintptr_t start = info->dlpi_addr + ph->p_vaddr;
      low = start < low ? start : low;
      high = start + ph->p_memsz > high ? start + ph->p_memsz : high;
    }
  }
  executable_low = low;
  executable_high = high;
  return 1; /* the first module is the executable */
}

static void at_exit(void);
static void retire_thread(void *data);

static void init_process(void) {
  const char *options = getenv("PTS_PUBLICATION_CHECK");
  if (options != NULL) {
    strict_mode = strstr(options, "strict") != NULL;
    report_all = strstr(options, "all") != NULL;
    track_uncollectable = strstr(options, "uncollectable") != NULL;
    check_malloc = strstr(options, "malloc") != NULL;
    report_partial = strstr(options, "partial") != NULL;
    print_stats = strstr(options, "stats") != NULL;
    report_none = strstr(options, "count") != NULL; /* count, report nothing but the total: for timing */
    report_all = report_all || report_none;
  }
  dl_iterate_phdr(find_executable, NULL);
  pthread_key_create(&exit_key, retire_thread);
  atexit(at_exit);
}

static int find_tls(struct dl_phdr_info *info, size_t size, void *data) {
  (void)size;
  checker_thread *t = data;
  for (int i = 0; i < info->dlpi_phnum; i++) {
    if (info->dlpi_phdr[i].p_type == PT_TLS && info->dlpi_tls_data != NULL) {
      t->tls_low = (uintptr_t)info->dlpi_tls_data;
      t->tls_high = t->tls_low + info->dlpi_phdr[i].p_memsz;
    }
  }
  return 1;
}

static checker_thread *start_thread(void) {
  pthread_once(&init_once, init_process);
  checker_thread *t = mmap(NULL, sizeof *t, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
  if (t == MAP_FAILED) {
    dprintf(STDERR_FILENO, "pts publication check: out of memory\n");
    _exit(VIOLATION_EXIT);
  }
  t->epoch = 1;
  t->fresh_low = UINTPTR_MAX;
  t->gc_no = GC_get_gc_no();
  pthread_attr_t attributes;
  if (pthread_getattr_np(pthread_self(), &attributes) == 0) {
    void *low;
    size_t size;
    if (pthread_attr_getstack(&attributes, &low, &size) == 0) {
      t->stack_low = (uintptr_t)low;
      t->stack_high = (uintptr_t)low + size;
    }
    pthread_attr_destroy(&attributes);
  }
  dl_iterate_phdr(find_tls, t);
  pthread_setspecific(exit_key, t);
  pthread_mutex_lock(&report_lock);
  t->next_thread = live_threads;
  live_threads = t;
  pthread_mutex_unlock(&report_lock);
  self = t;
  return t;
}

/* The calling thread's state, or NULL while it is in the checker already or has retired. */
static checker_thread *enter(void) {
  if (busy) {
    return NULL;
  }
  busy = 1;
  return self != NULL ? self : start_thread();
}

static void leave(void) { busy = 0; }

/* ---------------------------------------------------------------- the table */

static size_t hash_slot(uintptr_t base) { return (size_t)(((base >> 4) * UINT64_C(0x9E3779B97F4A7C15)) >> 48) & (HASH_SIZE - 1); }

static fresh_object *find_base(checker_thread *t, uintptr_t base) {
  for (size_t slot = hash_slot(base);; slot = (slot + 1) & (HASH_SIZE - 1)) {
    uint32_t entry = t->hash[slot];
    if (entry == 0) {
      return NULL;
    }
    if (t->ring[entry - 1].base == base) {
      return &t->ring[entry - 1];
    }
  }
}

static void hash_insert(checker_thread *t, uintptr_t base, uint32_t index) {
  size_t slot = hash_slot(base);
  while (t->hash[slot] != 0) {
    slot = (slot + 1) & (HASH_SIZE - 1);
  }
  t->hash[slot] = index + 1;
}

/* Removes base's entry from the hash, shifting later entries of its run back (no tombstones). */
static void hash_remove(checker_thread *t, uintptr_t base) {
  size_t slot = hash_slot(base);
  while (t->hash[slot] != 0 && t->ring[t->hash[slot] - 1].base != base) {
    slot = (slot + 1) & (HASH_SIZE - 1);
  }
  if (t->hash[slot] == 0) {
    return;
  }
  t->hash[slot] = 0;
  for (size_t next = (slot + 1) & (HASH_SIZE - 1); t->hash[next] != 0; next = (next + 1) & (HASH_SIZE - 1)) {
    size_t home = hash_slot(t->ring[t->hash[next] - 1].base);
    bool movable = slot <= next ? (home <= slot || home > next) : (home <= slot && home > next);
    if (movable) {
      t->hash[slot] = t->hash[next];
      t->hash[next] = 0;
      slot = next;
    }
  }
}

static void forget(checker_thread *t, fresh_object *object) {
  if (object->base == 0) {
    return;
  }
  hash_remove(t, object->base);
  free(object->written_words);
  if (t->last == object) {
    t->last = NULL;
  }
  memset(object, 0, sizeof *object);
}

/* The fresh object of t that address points into, or NULL. */
static fresh_object *lookup(checker_thread *t, uintptr_t address) {
  if (address < t->fresh_low || address >= t->fresh_high) {
    return NULL;
  }
  fresh_object *last = t->last;
  if (last != NULL && address - last->base < last->size) {
    return last;
  }
  uintptr_t base = (uintptr_t)GC_base((void *)address);
  if (base == 0) {
    return NULL;
  }
  fresh_object *object = find_base(t, base);
  if (object != NULL) {
    t->last = object;
  }
  return object;
}

/* Collections free fresh objects that died, and another thread may get their memory: entries of objects the last collection didn't mark go. */
static void revalidate(checker_thread *t) {
  GC_alloc_lock();
  t->fresh_low = UINTPTR_MAX;
  t->fresh_high = 0;
  for (size_t i = 0; i < RING_SIZE; i++) {
    fresh_object *object = &t->ring[i];
    if (object->base == 0 || (object->flags & UNCOLLECTABLE)) {
      continue;
    }
    if (GC_base((void *)object->base) != (void *)object->base || !GC_is_marked((void *)object->base)) {
      forget(t, object);
    }
  }
  for (size_t i = 0; i < RING_SIZE; i++) {
    const fresh_object *object = &t->ring[i];
    if (object->base != 0) {
      t->fresh_low = object->base < t->fresh_low ? object->base : t->fresh_low;
      t->fresh_high = object->base + object->size > t->fresh_high ? object->base + object->size : t->fresh_high;
    }
  }
  t->gc_no = GC_get_gc_no();
  GC_alloc_unlock();
}

static void check_collections(checker_thread *t) {
  if (GC_get_gc_no() != t->gc_no) {
    revalidate(t);
  }
}

/* Up to count return addresses of the callers of the function whose frame is fp (frame pointers). */
static void caller_pcs(checker_thread *t, uintptr_t *fp, uintptr_t *pcs, int count) {
  for (int i = 0; i < count; i++) {
    pcs[i] = 0;
  }
  for (int i = 0; i < count; i++) {
    if ((uintptr_t)fp < t->stack_low || (uintptr_t)fp + 2 * sizeof(uintptr_t) > t->stack_high || ((uintptr_t)fp & 7) != 0) {
      return;
    }
    pcs[i] = fp[1];
    uintptr_t *caller = (uintptr_t *)fp[0];
    if (caller <= fp) {
      return;
    }
    fp = caller;
  }
}

static void add_fresh(checker_thread *t, void *memory, size_t size, uint32_t flags, void *fp) {
  if (memory == NULL) {
    return;
  }
  uintptr_t base = (uintptr_t)memory;
  fresh_object *old = find_base(t, base);
  if (old != NULL) {
    forget(t, old);
  }
  size_t index = t->next;
  t->next = (t->next + 1) & (RING_SIZE - 1);
  fresh_object *object = &t->ring[index];
  forget(t, object);
  object->base = base;
  object->size = size;
  object->dirty_epoch = t->epoch;
  object->flags = flags;
  size_t words = (size + 7) / 8;
  if (words > INLINE_WORDS && words <= MAX_BITMAP_WORDS) {
    object->written_words = calloc((words + 63) / 64, sizeof(uint64_t));
  }
  caller_pcs(t, (uintptr_t *)fp, object->site, SITE_FRAMES);
  t->stats.allocations++;
  hash_insert(t, base, (uint32_t)index);
  t->fresh_low = base < t->fresh_low ? base : t->fresh_low;
  t->fresh_high = base + size > t->fresh_high ? base + size : t->fresh_high;
}

static bool is_dirty(const checker_thread *t, const fresh_object *object) { return object->dirty_epoch == t->epoch; }

/* Marks word as written, returning whether a store had written it before. */
static bool mark_written(fresh_object *object, size_t word) {
  uint64_t bit = UINT64_C(1) << (word % 64);
  uint64_t *bits = NULL;
  if (object->size <= INLINE_WORDS * 8) {
    bits = &object->written;
  } else if (object->written_words != NULL) {
    bits = &object->written_words[word / 64];
  }
  if (bits == NULL) {
    return false;
  }
  bool before = (*bits & bit) != 0;
  *bits |= bit;
  return before;
}

static bool all_zero(const unsigned char *bytes, size_t size) {
  for (size_t i = 0; i < size; i++) {
    if (bytes[i] != 0) {
      return false;
    }
  }
  return true;
}

/* A store of size bytes at address, inside object, before it happens: a fill makes object dirty. */
static void fill(checker_thread *t, fresh_object *object, uintptr_t address, size_t size) {
  size_t first = (address - object->base) / 8, last = (address + size - 1 - object->base) / 8;
  bool written_before = true;
  for (size_t word = first; word <= last; word++) {
    written_before &= mark_written(object, word);
  }
  bool first_store = !written_before && all_zero((const unsigned char *)address, size); /* memcpy and memset fill words unseen */
  first_store &= !(object->flags & SLOTS); /* a hole or a zero there is a value readers take */
  t->stats.fills += first_store;
  if (strict_mode || first_store) {
    object->dirty_epoch = t->epoch;
  }
}

/* ---------------------------------------------------------------- publication */

static bool is_private(const checker_thread *t, uintptr_t address) {
  if ((address >= t->stack_low && address < t->stack_high) || (address >= t->tls_low && address < t->tls_high)) {
    return true;
  }
  for (size_t i = 0; i < t->private_count; i++) {
    if (address >= t->private_low[i] && address < t->private_high[i]) {
      return true;
    }
  }
  return false;
}

/*
 * object has become reachable from shared memory: it stops being fresh, and so does every
 * fresh object it holds a pointer to, which mustn't be dirty either (unless released).
 */
static void share(checker_thread *t, fresh_object *object, bool released, uintptr_t address, const uintptr_t *store_pcs) {
  uintptr_t stack[SHARE_STACK][3]; /* base, size, holds pointers */
  size_t depth = 0;
  stack[depth][0] = object->base;
  stack[depth][1] = object->size;
  stack[depth][2] = object->flags & HOLDS_POINTERS;
  depth++;
  forget(t, object);
  while (depth > 0) {
    depth--;
    uintptr_t base = stack[depth][0], size = stack[depth][1];
    if (!stack[depth][2]) {
      continue;
    }
    for (uintptr_t word = base; word + 8 <= base + size; word += 8) {
      fresh_object *held = lookup(t, *(const uintptr_t *)word);
      if (held == NULL) {
        continue;
      }
      if (!released && is_dirty(t, held)) {
        fresh_object via = {.base = base, .size = size};
        report_violation(t, held, address, store_pcs, &via);
      }
      if (depth < SHARE_STACK) {
        stack[depth][0] = held->base;
        stack[depth][1] = held->size;
        stack[depth][2] = held->flags & HOLDS_POINTERS;
        depth++;
      }
      forget(t, held);
    }
  }
}

/* Whether address is in the stack of a thread other than t, for a caller that holds report_lock. */
static bool in_other_stack_locked(const checker_thread *t, uintptr_t address) {
  for (const checker_thread *other = live_threads; other != NULL; other = other->next_thread) {
    if (other != t && address >= other->stack_low && address < other->stack_high) {
      return true;
    }
  }
  return false;
}

static bool in_other_stack(const checker_thread *t, uintptr_t address) {
  pthread_mutex_lock(&report_lock);
  bool found = in_other_stack_locked(t, address);
  pthread_mutex_unlock(&report_lock);
  return found;
}

/*
 * Whether address is memory that other threads read: static data, the collected heap
 * (outside t's fresh objects) or another thread's stack. The rest (malloc's) holds no
 * collected pointers but a thread's scratch copies, as bdwgc doesn't scan it (inspect's
 * list of the objects it has seen): private, unless PTS_PUBLICATION_CHECK=malloc.
 */
static bool is_shared(const checker_thread *t, uintptr_t address) {
  return check_malloc || (address >= executable_low && address < executable_high) || GC_base((void *)address) != NULL ||
         in_other_stack(t, address);
}

/* The value now at address, which a store left there, publishes what it points to if address is shared. */
static void check_published(checker_thread *t, uintptr_t address, size_t size, const uintptr_t *store_pcs, bool released) {
  for (size_t offset = 0; offset + 8 <= size; offset += 8) {
    fresh_object *object = lookup(t, *(const uintptr_t *)(address + offset));
    if (object == NULL || !is_shared(t, address + offset)) {
      continue;
    }
    if (!released && is_dirty(t, object)) {
      report_violation(t, object, address + offset, store_pcs, NULL);
    }
    t->stats.publications++;
    share(t, object, released, address + offset, store_pcs);
  }
}

static void finish_pending(checker_thread *t) {
  pending_store pending = t->pending;
  if (pending.address == 0) {
    return;
  }
  t->pending.address = 0;
  check_published(t, pending.address, pending.size, pending.pc, false);
}

/* A store of size bytes at address, by the code that called the hook whose frame is fp. */
static inline __attribute__((always_inline)) void on_store(uintptr_t address, size_t size, uintptr_t *fp) {
  checker_thread *t = enter();
  if (t == NULL) {
    return;
  }
  finish_pending(t);
  if (!is_private(t, address)) {
    t->stats.heap_stores++;
    check_collections(t);
    fresh_object *object = lookup(t, address);
    if (object != NULL) {
      fill(t, object, address, size);
    } else if (size >= 8) {
      t->pending.address = address;
      t->pending.size = size;
      caller_pcs(t, fp, t->pending.pc, STORE_FRAMES);
    }
  }
  leave();
}

#define STORE_HOOK(bytes, type)                                                                   \
  void __sanitizer_cov_store##bytes(type *address) {                                               \
    on_store((uintptr_t)address, bytes, (uintptr_t *)__builtin_frame_address(0));                 \
  }

STORE_HOOK(1, uint8_t)
STORE_HOOK(2, uint16_t)
STORE_HOOK(4, uint32_t)
STORE_HOOK(8, uint64_t)
STORE_HOOK(16, __int128)

void pts_publication_barrier_hook(void) {
  checker_thread *t = enter();
  if (t == NULL) {
    return;
  }
  finish_pending(t);
  t->epoch++;
  t->stats.barriers++;
  leave();
}

void pts_publication_check_store(const volatile void *address) {
  checker_thread *t = enter();
  if (t == NULL) {
    return;
  }
  finish_pending(t);
  uintptr_t at = (uintptr_t)address;
  if (!is_private(t, at)) {
    check_collections(t);
    fresh_object *object = lookup(t, at);
    if (object != NULL) {
      mark_written(object, (at - object->base) / 8);
      object->dirty_epoch = t->epoch;
    } else {
      uintptr_t pcs[STORE_FRAMES];
      caller_pcs(t, (uintptr_t *)__builtin_frame_address(0), pcs, STORE_FRAMES);
      check_published(t, at, 8, pcs, false);
    }
  }
  leave();
}

void pts_publication_check_private(const volatile void *object) {
  checker_thread *t = enter();
  if (t == NULL) {
    return;
  }
  void *base = GC_base((void *)object);
  if (base != NULL && t->private_count < MAX_PRIVATE) {
    t->private_low[t->private_count] = (uintptr_t)base;
    t->private_high[t->private_count] = (uintptr_t)base + GC_size(base);
    t->private_count++;
  }
  leave();
}

void pts_publication_check_slots(const volatile void *object) {
  checker_thread *t = enter();
  if (t == NULL) {
    return;
  }
  fresh_object *fresh = lookup(t, (uintptr_t)object);
  if (fresh != NULL) {
    fresh->flags |= SLOTS;
  }
  leave();
}

/* Makes the fresh object that address points into clean, if there is one. */
static void make_clean(checker_thread *t, uintptr_t address) {
  fresh_object *object = lookup(t, address);
  if (object != NULL) {
    object->dirty_epoch = 0;
  }
}

/*
 * A release store at address, just made: the stores before it reach the threads that read
 * address with acquire (or through the address it holds), which see the object holding
 * address, the one it points to and also's object filled. So those are clean, and what the
 * store published is shared, not a violation. Other objects stay as they are: a release
 * doesn't order the stores before it with the plain stores after it. That holds for the
 * three objects too, which a thread reaching them through a plain store made after the
 * release may see as they were before it (an array before its last pushes: never garbage,
 * since its length is published after its elements); PTS_PUBLICATION_CHECK=partial reports
 * that too, leaving them as they are.
 */
void pts_publication_check_release(const volatile void *address, const volatile void *also) {
  checker_thread *t = enter();
  if (t == NULL) {
    return;
  }
  uintptr_t at = (uintptr_t)address;
  if (t->pending.address == at) {
    t->pending.address = 0; /* the release store itself, checked below */
  }
  finish_pending(t);
  if (!is_private(t, at)) {
    check_collections(t);
    bool in_fresh = lookup(t, at) != NULL;
    if (!report_partial) {
      make_clean(t, at);
      make_clean(t, *(const uintptr_t *)at);
      make_clean(t, (uintptr_t)also);
    }
    if (!in_fresh) {
      check_published(t, at, 8, NULL, true);
    }
  }
  leave();
}

/* ---------------------------------------------------------------- allocation */

void *__real_GC_malloc(size_t size);
void *__real_GC_malloc_atomic(size_t size);
void *__real_GC_gcj_malloc(size_t size, void *descriptor);
void *__real_GC_malloc_uncollectable(size_t size);
void *__real_GC_realloc(void *old, size_t size);
void __real_GC_free(void *memory);

/* Before an allocation, which may collect. */
static checker_thread *before_allocation(void) {
  checker_thread *t = enter();
  if (t != NULL) {
    finish_pending(t);
    check_collections(t);
  }
  return t;
}

/* After an allocation by the wrap whose frame is fp. */
static void after_allocation(checker_thread *t, void *memory, size_t size, uint32_t flags, void *fp) {
  if (t == NULL) {
    return;
  }
  check_collections(t);
  add_fresh(t, memory, size, flags, fp);
  leave();
}

void *__wrap_GC_malloc(size_t size) {
  checker_thread *t = before_allocation();
  void *memory = __real_GC_malloc(size);
  after_allocation(t, memory, size, HOLDS_POINTERS, __builtin_frame_address(0));
  return memory;
}

/*
 * bdwgc doesn't zero atomic memory: the checker does (no correct program reads it first),
 * so that a word's first store is told from a change by the word holding zero.
 */
void *__wrap_GC_malloc_atomic(size_t size) {
  checker_thread *t = before_allocation();
  void *memory = __real_GC_malloc_atomic(size);
  if (memory != NULL) {
    memset(memory, 0, size);
  }
  after_allocation(t, memory, size, 0, __builtin_frame_address(0));
  return memory;
}

void *__wrap_GC_gcj_malloc(size_t size, void *descriptor) {
  checker_thread *t = before_allocation();
  void *memory = __real_GC_gcj_malloc(size, descriptor);
  after_allocation(t, memory, size, HOLDS_POINTERS, __builtin_frame_address(0));
  if (t != NULL && memory != NULL) {
    fresh_object *object = find_base(t, (uintptr_t)memory);
    if (object != NULL) {
      mark_written(object, 0); /* bdwgc stored the descriptor */
    }
  }
  return memory;
}

/*
 * Uncollectable memory holds the runtime's own structures (loops, deques, workers, queues,
 * servers), which threads reach by pthread_once, locks and thread creation: tracked only
 * with PTS_PUBLICATION_CHECK=uncollectable.
 */
void *__wrap_GC_malloc_uncollectable(size_t size) {
  checker_thread *t = before_allocation();
  void *memory = __real_GC_malloc_uncollectable(size);
  if (t != NULL && !track_uncollectable) {
    leave();
    return memory;
  }
  after_allocation(t, memory, size, HOLDS_POINTERS | UNCOLLECTABLE, __builtin_frame_address(0));
  return memory;
}

void *__wrap_GC_realloc(void *old, size_t size) {
  checker_thread *t = before_allocation();
  if (t != NULL && old != NULL) {
    fresh_object *object = find_base(t, (uintptr_t)old);
    if (object != NULL) {
      forget(t, object);
    }
  }
  void *memory = __real_GC_realloc(old, size);
  after_allocation(t, memory, size, HOLDS_POINTERS, __builtin_frame_address(0));
  return memory;
}

void __wrap_GC_free(void *memory) {
  checker_thread *t = enter();
  if (t != NULL) {
    finish_pending(t);
    fresh_object *object = memory != NULL ? find_base(t, (uintptr_t)memory) : NULL;
    if (object != NULL) {
      forget(t, object);
    }
    leave();
  }
  __real_GC_free(memory);
}

/* ---------------------------------------------------------------- locks */

/*
 * A store made while the thread holds a pthread mutex (or runs a pthread_once routine)
 * reaches other threads through that lock, whose readers take it too (the inboxes, the
 * fetch table, the once-made loops and pools): what it publishes is shared, not a violation.
 */
int __real_pthread_mutex_lock(pthread_mutex_t *mutex);
int __real_pthread_mutex_trylock(pthread_mutex_t *mutex);
int __real_pthread_mutex_unlock(pthread_mutex_t *mutex);
int __real_pthread_once(pthread_once_t *once, void (*routine)(void));

/* The store before a lock is taken, which the lock doesn't order. */
static void finish_before_lock(void) {
  checker_thread *t = enter();
  if (t != NULL) {
    finish_pending(t);
    leave();
  }
}

int __wrap_pthread_mutex_lock(pthread_mutex_t *mutex) {
  finish_before_lock();
  int result = __real_pthread_mutex_lock(mutex);
  locks_held += result == 0;
  return result;
}

int __wrap_pthread_mutex_trylock(pthread_mutex_t *mutex) {
  finish_before_lock();
  int result = __real_pthread_mutex_trylock(mutex);
  locks_held += result == 0;
  return result;
}

int __wrap_pthread_mutex_unlock(pthread_mutex_t *mutex) {
  checker_thread *t = enter();
  if (t != NULL) {
    finish_pending(t); /* a store made under the lock */
    leave();
  }
  locks_held -= locks_held > 0;
  return __real_pthread_mutex_unlock(mutex);
}

int __wrap_pthread_once(pthread_once_t *once, void (*routine)(void)) {
  finish_before_lock();
  locks_held++;
  int result = __real_pthread_once(once, routine);
  checker_thread *t = enter();
  if (t != NULL) {
    finish_pending(t);
    leave();
  }
  locks_held--;
  return result;
}

/* ---------------------------------------------------------------- exit */

/* Adds stats to totals, under report_lock. */
static void add_stats(checker_stats *sum, const checker_stats *stats) {
  sum->allocations += stats->allocations;
  sum->heap_stores += stats->heap_stores;
  sum->fills += stats->fills;
  sum->publications += stats->publications;
  sum->barriers += stats->barriers;
}

static void retire_thread(void *data) {
  checker_thread *t = data;
  busy = 1; /* for good: later callbacks on this thread are ignored */
  finish_pending(t);
  pthread_mutex_lock(&report_lock);
  add_stats(&totals, &t->stats);
  for (checker_thread **link = &live_threads; *link != NULL; link = &(*link)->next_thread) {
    if (*link == t) {
      *link = t->next_thread;
      break;
    }
  }
  pthread_mutex_unlock(&report_lock);
  for (size_t i = 0; i < RING_SIZE; i++) {
    free(t->ring[i].written_words);
  }
  self = NULL;
  munmap(t, sizeof *t);
}

static void at_exit(void) {
  checker_thread *t = enter();
  if (t != NULL) {
    finish_pending(t);
    leave();
  }
  pthread_mutex_lock(&report_lock);
  size_t count = violations;
  if (print_stats) {
    checker_stats sum = totals;
    for (const checker_thread *live = live_threads; live != NULL; live = live->next_thread) {
      add_stats(&sum, &live->stats); /* racy reads of threads still running: a count */
    }
    dprintf(STDERR_FILENO, "pts publication check: %zu allocations, %zu stores into the heap or static data, %zu fills, %zu publications, %zu barriers\n",
            sum.allocations, sum.heap_stores, sum.fills, sum.publications, sum.barriers);
  }
  pthread_mutex_unlock(&report_lock);
  if (count > 0) {
    dprintf(STDERR_FILENO, "pts publication check: %zu publication violation(s) in %zu place(s)\n", count, reported_count);
    _exit(VIOLATION_EXIT);
  }
}

/* ---------------------------------------------------------------- reports */

static void ignore_error(void *data, const char *message, int number) {
  (void)data;
  (void)message;
  (void)number;
}

static int print_frame(void *data, uintptr_t pc, const char *file, int line, const char *function) {
  (void)pc;
  int *printed = data;
  dprintf(STDERR_FILENO, "      %s%s (%s:%d)\n", *printed > 0 ? "inlined into " : "", function != NULL ? function : "?", file != NULL ? file : "?", line);
  ++*printed;
  return 0;
}

static void print_symbol(void *data, uintptr_t pc, const char *name, uintptr_t value, uintptr_t size) {
  (void)pc;
  (void)size;
  uintptr_t address = *(uintptr_t *)data;
  if (name != NULL) {
    dprintf(STDERR_FILENO, "global %s+%zu", name, (size_t)(address - value));
  } else {
    dprintf(STDERR_FILENO, "static data at %#zx", (size_t)address);
  }
}

static void print_pcs(const uintptr_t *pcs, int count) {
  for (int i = 0; i < count && pcs[i] != 0; i++) {
    int printed = 0;
    backtrace_pcinfo(symbols, pcs[i] - 1, print_frame, ignore_error, &printed);
    if (printed == 0) {
      dprintf(STDERR_FILENO, "      %#zx\n", (size_t)pcs[i]);
    }
  }
}

/* What a heap object's first word says it is: a runtime type's name, if it holds one. */
static const char *type_name(uintptr_t base) {
  uintptr_t type = *(const uintptr_t *)base;
  if (type < executable_low || type >= executable_high) {
    return NULL;
  }
  const char *name = ((const pts_type *)type)->name;
  if ((uintptr_t)name < executable_low || (uintptr_t)name >= executable_high) {
    return NULL;
  }
  return name[0] != '\0' ? name : "(anonymous)";
}

static void print_place(const checker_thread *t, uintptr_t address) {
  if (address >= executable_low && address < executable_high) {
    backtrace_syminfo(symbols, address, print_symbol, ignore_error, &address);
    return;
  }
  uintptr_t base = (uintptr_t)GC_base((void *)address);
  if (base == 0) {
    dprintf(STDERR_FILENO, "%s at %#zx", in_other_stack_locked(t, address) ? "another thread's stack" : "malloc's memory", (size_t)address);
    return;
  }
  const char *name = type_name(base);
  dprintf(STDERR_FILENO, "word %zu of the heap object %#zx%s%s%s", (size_t)(address - base) / 8, (size_t)base, name ? " (" : "", name ? name : "", name ? ")" : "");
}

static bool already_reported(uintptr_t store_pc, uintptr_t site) {
  for (size_t i = 0; i < reported_count; i++) {
    if (reported[i][0] == store_pc && reported[i][1] == site) {
      return true;
    }
  }
  if (reported_count < MAX_REPORTED) {
    reported[reported_count][0] = store_pc;
    reported[reported_count][1] = site;
    reported_count++;
  }
  return false;
}

static void report_violation(checker_thread *t, const fresh_object *object, uintptr_t address, const uintptr_t *store_pcs, const fresh_object *via) {
  if (locks_held > 0) {
    return; /* published under a lock, which its readers take too */
  }
  pthread_mutex_lock(&report_lock);
  violations++;
  uintptr_t store_pc = store_pcs != NULL ? store_pcs[0] : 0;
  if (report_none || already_reported(store_pc, object->site[1])) {
    pthread_mutex_unlock(&report_lock);
    return;
  }
  if (symbols == NULL) {
    symbols = backtrace_create_state(NULL, 1, ignore_error, NULL);
  }
  const char *name = type_name(object->base);
  dprintf(STDERR_FILENO, "pts publication violation: a new object reached memory other threads can read with words written after the last publication barrier\n");
  dprintf(STDERR_FILENO, "  object %#zx (%zu bytes%s%s%s), allocated at\n", (size_t)object->base, object->size, name ? ", " : "", name ? name : "", "");
  print_pcs(object->site, SITE_FRAMES);
  if (via != NULL) {
    const char *via_name = type_name(via->base);
    dprintf(STDERR_FILENO, "  is held by the object %#zx%s%s%s, which this store published\n", (size_t)via->base, via_name ? " (" : "", via_name ? via_name : "", via_name ? ")" : "");
  }
  dprintf(STDERR_FILENO, "  stored into ");
  print_place(t, address);
  dprintf(STDERR_FILENO, ", at\n");
  if (store_pcs != NULL) {
    print_pcs(store_pcs, STORE_FRAMES);
  }
  if (!report_all) {
    _exit(VIOLATION_EXIT);
  }
  pthread_mutex_unlock(&report_lock);
}

#pragma clang attribute pop

#else
typedef int pts_publication_check_unused; /* ISO C needs a declaration in a translation unit */
#endif
