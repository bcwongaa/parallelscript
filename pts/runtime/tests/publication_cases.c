/*
 * Cases for the publication checker (publication_check.c, H35b), built with
 * toolchain.PublicationCheck by pts/toolchain's TestPublicationChecker*: the argument
 * names one; the program exits 0, or the checker ends it with its report (exit 70).
 */
#include "pts.h"

#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct box {
  uint64_t a, b;
  struct box *next;
} box;

/* What other threads could read; volatile, so that clang keeps each store to it. */
static box *volatile published;
static volatile int steps;

static box *new_box(uint64_t a) {
  box *b = GC_MALLOC(sizeof *b);
  b->a = a;
  b->b = a + 1;
  return b;
}

static void unfenced(void) { published = new_box(1); }

static void fenced(void) {
  box *b = new_box(1);
  pts_publication_barrier_hook();
  published = b;
}

/* Stores into the stack and into other new objects publish nothing. */
static void private_stores(void) {
  box *volatile local[2];
  local[0] = new_box(1);
  box *outer = new_box(2);
  outer->next = new_box(3);
  outer->next->next = local[0];
  local[1] = outer;
  printf("%llu\n", (unsigned long long)(local[1]->next->next->a + local[0]->b));
}

/* A new object written after the barrier, reached through the clean one the store publishes. */
static void held_dirty(void) {
  box *outer = new_box(1);
  box *inner = new_box(2);
  outer->next = inner;
  pts_publication_barrier_hook();
  inner->next = inner;
  published = outer;
}

/* A store into a word written before the barrier changes a filled object: no fill, unless strict. */
static void mutation(void) {
  box *b = new_box(1);
  pts_publication_barrier_hook();
  b->a = 5;
  published = b;
}

static void release(void) {
  box *b = new_box(1);
  __atomic_store_n(&published, b, __ATOMIC_RELEASE);
  PTS_PUBLICATION_RELEASE(&published, NULL);
}

static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;

/* A store under a lock reaches the threads that take the lock to read it. */
static void locked(void) {
  pthread_mutex_lock(&lock);
  published = new_box(1);
  pthread_mutex_unlock(&lock);
}

/* An unlock doesn't order the stores before it with a plain store after it. */
static void after_unlock(void) {
  pthread_mutex_lock(&lock);
  box *b = new_box(1);
  pthread_mutex_unlock(&lock);
  published = b;
}

/* A compare-and-swap isn't a store clang reports: PTS_PUBLICATION_STORE announces it. */
static void compare_and_swap(void) {
  box *b = new_box(1);
  box *expected = NULL;
  if (__atomic_compare_exchange_n(&published, &expected, b, false, __ATOMIC_RELAXED, __ATOMIC_RELAXED)) {
    PTS_PUBLICATION_STORE(&published);
  }
}

static void *publish_and_end(void *unused) {
  (void)unused;
  published = new_box(1); /* the thread's last store: checked when it ends */
  return NULL;
}

static void thread_end(void) {
  pthread_t thread;
  pthread_create(&thread, NULL, publish_and_end, NULL);
  pthread_join(thread, NULL);
  printf("joined\n");
}

/* malloc's memory holds scratch copies only (bdwgc doesn't scan it): private, unless PTS_PUBLICATION_CHECK=malloc. */
static void malloc_scratch(void) {
  box *volatile *scratch = malloc(sizeof *scratch);
  scratch[0] = new_box(1);
  steps = 1; /* the checker checks a store at the thread's next one: here, before free */
  printf("%llu\n", (unsigned long long)scratch[0]->b);
  free((void *)scratch);
}

static void *publish_into(void *slot) {
  *(box *volatile *)slot = new_box(1); /* into the stack of the thread that waits for this one */
  return NULL;
}

static void other_stack(void) {
  box *volatile slot = NULL;
  pthread_t thread;
  pthread_create(&thread, NULL, publish_into, (void *)&slot);
  pthread_join(thread, NULL);
  printf("%llu\n", (unsigned long long)slot->a);
}

static void *volatile published_array;

/*
 * A new array filled by pushes, whose length and buffer the runtime publishes with release
 * stores, then published by a plain store: a thread may see it with fewer elements (never
 * garbage). Reported only with PTS_PUBLICATION_CHECK=partial.
 */
static void pushed(void) {
  pts_array *array = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], NULL, 0);
  pts_publication_barrier_hook(); /* as pts_array_of's own pts_publish does, without the checker's pts.h macros here */
  for (int i = 0; i < 20; i++) {
    pts_array_push(array, (double[]){i}, 1);
  }
  published_array = array;
}

/* Elements whose zero is a value readers take (an array's holes): a first store after the barrier changes them. */
static void fill_after_barrier(bool marked) {
  box *elements = GC_MALLOC(sizeof *elements);
  if (marked) {
    PTS_PUBLICATION_SLOTS(elements);
  }
  pts_publication_barrier_hook();
  elements->a = 7;
  published = elements;
}

static void slots(void) { fill_after_barrier(true); }

static void unmarked_slots(void) { fill_after_barrier(false); }

/* A thread's own record, which only a thread-local variable holds, publishes nothing stored into it. */
static _Thread_local box *record;

static void store_into_record(bool marked) {
  record = GC_MALLOC_UNCOLLECTABLE(sizeof *record);
  if (marked) {
    PTS_PUBLICATION_PRIVATE(record);
  }
  record->next = new_box(1);
}

static void private_record(void) { store_into_record(true); }

static void shared_record(void) { store_into_record(false); }

static const struct {
  const char *name;
  void (*run)(void);
} cases[] = {
    {"unfenced", unfenced},     {"fenced", fenced},
    {"private", private_stores}, {"held_dirty", held_dirty},
    {"mutation", mutation},     {"release", release},
    {"locked", locked},         {"after_unlock", after_unlock},
    {"compare_and_swap", compare_and_swap}, {"thread_end", thread_end},
    {"malloc_scratch", malloc_scratch}, {"other_stack", other_stack},
    {"pushed", pushed},         {"slots", slots},
    {"unmarked_slots", unmarked_slots}, {"private_record", private_record},
    {"shared_record", shared_record},
};

int main(int argc, char **argv) {
  pts_runtime_init(argc, argv);
  for (size_t i = 0; argc == 2 && i < sizeof cases / sizeof cases[0]; i++) {
    if (strcmp(argv[1], cases[i].name) == 0) {
      cases[i].run();
      return 0;
    }
  }
  fprintf(stderr, "usage: %s <case>\n", argv[0]);
  return 2;
}
