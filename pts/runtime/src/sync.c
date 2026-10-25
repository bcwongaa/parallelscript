/*
 * pts:sync's intrinsics (D-128): 64-bit atomic cells and locks, each in the first bytes
 * of an ArrayBuffer the library made for it (aligned to 16, never moved). Every atomic
 * operation is sequentially consistent, as JS's Atomics are. A lock is Drepper's futex
 * mutex ("Futexes Are Tricky", mutex 3): 0 free, 1 held, 2 held with threads waiting; any
 * thread may release it, so a lock can pass from the thread that took it to another.
 */
#include "pts.h"

#include <linux/futex.h>
#include <sys/syscall.h>
#include <unistd.h>

static int64_t *cell_of(pts_arraybuffer *buffer) { return (int64_t *)buffer->bytes; }

static uint32_t *word_of(pts_arraybuffer *buffer) { return (uint32_t *)buffer->bytes; }

int64_t pts_atomic_load(pts_arraybuffer *cell) { return __atomic_load_n(cell_of(cell), __ATOMIC_SEQ_CST); }

void pts_atomic_store(pts_arraybuffer *cell, int64_t value) { __atomic_store_n(cell_of(cell), value, __ATOMIC_SEQ_CST); }

int64_t pts_atomic_add(pts_arraybuffer *cell, int64_t delta) {
  /* in uint64_t, so that it wraps as ParallelScript's integers do (D-012) */
  return (int64_t)__atomic_fetch_add((uint64_t *)cell_of(cell), (uint64_t)delta, __ATOMIC_SEQ_CST);
}

int64_t pts_atomic_compare_exchange(pts_arraybuffer *cell, int64_t expected, int64_t desired) {
  __atomic_compare_exchange_n(cell_of(cell), &expected, desired, false, __ATOMIC_SEQ_CST, __ATOMIC_SEQ_CST);
  return expected; /* the value before: expected itself if it was there */
}

/* How many times a lock is tried before its thread sleeps: short critical sections end sooner than a sleep. */
enum { SPINS = 100 };

/*
 * Tells the core that this thread spins on a lock: x86's PAUSE or arm's YIELD, hints that
 * let the core's other hardware thread run. Most arm cores have none, and YIELD does about
 * nothing there; the lock sleeps on its futex after SPINS tries either way.
 */
static inline void spin_pause(void) {
#if defined(__x86_64__)
  __builtin_ia32_pause();
#elif defined(__aarch64__)
  __asm__ volatile("yield");
#else
#error "spin_pause: a processor ParallelScript doesn't target"
#endif
}

static void futex_wait(uint32_t *word, uint32_t value) { syscall(SYS_futex, word, FUTEX_WAIT_PRIVATE, value, NULL, NULL, 0); }

static void futex_wake(uint32_t *word) { syscall(SYS_futex, word, FUTEX_WAKE_PRIVATE, 1, NULL, NULL, 0); }

bool pts_sync_try_lock(pts_arraybuffer *lock) {
  uint32_t free = 0;
  return __atomic_compare_exchange_n(word_of(lock), &free, 1, false, __ATOMIC_ACQUIRE, __ATOMIC_RELAXED);
}

void pts_sync_lock(pts_arraybuffer *lock) {
  uint32_t *word = word_of(lock);
  for (int i = 0; i < SPINS; i++) {
    if (pts_sync_try_lock(lock)) {
      return;
    }
    spin_pause();
  }
  uint32_t seen = __atomic_exchange_n(word, 2, __ATOMIC_ACQUIRE);
  while (seen != 0) {
    futex_wait(word, 2); /* returns at once if the word isn't 2 any more, or on a signal (the collector's) */
    seen = __atomic_exchange_n(word, 2, __ATOMIC_ACQUIRE);
  }
}

void pts_sync_unlock(pts_arraybuffer *lock) {
  uint32_t *word = word_of(lock);
  if (__atomic_fetch_sub(word, 1, __ATOMIC_RELEASE) != 1) { /* it was 2: someone may be waiting */
    __atomic_store_n(word, 0, __ATOMIC_RELEASE);
    futex_wake(word);
  }
}
