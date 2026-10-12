/* pts:sync's intrinsics (D-128): atomic cells and locks used by several threads at once. */
#include "pts.h" /* first: it asks for the POSIX declarations */

#include "check.h"

#include <pthread.h>

enum { THREADS = 4, ROUNDS = 20000 };

static pts_arraybuffer *cell, *lock;
static int64_t guarded; /* changed only under lock */

static void *work(void *argument) {
  (void)argument;
  for (int i = 0; i < ROUNDS; i++) {
    pts_atomic_add(cell, 1);
    pts_sync_lock(lock);
    guarded++;
    pts_sync_unlock(lock);
  }
  return NULL;
}

static void threads_add_and_lock_without_losing_updates(void) {
  cell = pts_arraybuffer_new(8);
  lock = pts_arraybuffer_new(4);
  pthread_t threads[THREADS];
  for (int i = 0; i < THREADS; i++) {
    pthread_create(&threads[i], NULL, work, NULL);
  }
  for (int i = 0; i < THREADS; i++) {
    pthread_join(threads[i], NULL);
  }
  CHECK(pts_atomic_load(cell) == THREADS * ROUNDS);
  CHECK(guarded == THREADS * ROUNDS);
}

static void compare_exchange_returns_the_value_before(void) {
  pts_arraybuffer *c = pts_arraybuffer_new(8);
  pts_atomic_store(c, 5);
  CHECK(pts_atomic_compare_exchange(c, 4, 9) == 5 && pts_atomic_load(c) == 5);
  CHECK(pts_atomic_compare_exchange(c, 5, 9) == 5 && pts_atomic_load(c) == 9);
  pts_atomic_store(c, INT64_MAX);
  CHECK(pts_atomic_add(c, 1) == INT64_MAX && pts_atomic_load(c) == INT64_MIN); /* wraps */
}

static void another_thread_may_release_a_lock(void) {
  pts_arraybuffer *l = pts_arraybuffer_new(4);
  pts_sync_lock(l);
  CHECK(!pts_sync_try_lock(l));
  pthread_t other;
  pthread_create(&other, NULL, (void *(*)(void *))pts_sync_unlock, l);
  pthread_join(other, NULL);
  CHECK(pts_sync_try_lock(l));
  pts_sync_unlock(l);
}

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  threads_add_and_lock_without_losing_updates();
  compare_exchange_returns_the_value_before();
  another_thread_may_release_a_lock();
  return CHECK_DONE();
}
