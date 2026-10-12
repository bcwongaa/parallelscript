#include "pts.h"

#include <gc_gcj.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

/* The collector runs once the program has allocated as much as the heap holds (D-060), as
 * Go's default GOGC=100 does; bdwgc's own default (3) collects three times as often. The
 * GC_FREE_SPACE_DIVISOR environment variable still overrides it. */
enum { FREE_SPACE_DIVISOR = 1 };

/* The heap starts at 4 MiB, as Go's minimum heap target does, so that a program that keeps
 * little alive isn't collected every few hundred KiB (D-112). GC_INITIAL_HEAP_SIZE overrides it. */
enum { INITIAL_HEAP_SIZE = 4 << 20 };

/* Marking runs on this many threads (D-131): bdwgc's default, one per core, made threads
 * that allocate heavily slower on every count we measured, and one was slower than two
 * for one thread. The GC_MARKERS environment variable still overrides it. */
enum { GC_MARKER_THREADS = 2 };

void pts_runtime_init(int argc, char **argv) {
  pts_process_init(argc, argv);
  GC_set_markers_count(GC_MARKER_THREADS);
  GC_INIT();
  if (getenv("GC_FREE_SPACE_DIVISOR") == NULL) {
    GC_set_free_space_divisor(FREE_SPACE_DIVISOR);
  }
  if (getenv("GC_INITIAL_HEAP_SIZE") == NULL) {
    GC_expand_hp(INITIAL_HEAP_SIZE);
  }
  GC_allow_register_threads();
  GC_init_gcj_malloc(GC_GCJ_RESERVED_MARK_PROC_INDEX, NULL); /* objects: see pts_object_new */
  pts_time_origin_init();
  pts_stack_limit_init();
}

_Thread_local uintptr_t pts_stack_limit;

/* What the guard leaves of the stack for throwing RangeError: formatting it, allocating (and perhaps collecting) and capturing frames. */
enum { STACK_RESERVE = 256 * 1024 };

void pts_stack_limit_init(void) {
  pthread_attr_t attributes;
  if (pthread_getattr_np(pthread_self(), &attributes) != 0) {
    return; /* no limit: deep recursion crashes as before */
  }
  void *low;
  size_t size;
  if (pthread_attr_getstack(&attributes, &low, &size) == 0 && size > 2 * STACK_RESERVE) {
    pts_stack_limit = (uintptr_t)low + STACK_RESERVE;
  }
  pthread_attr_destroy(&attributes);
}

void pts_throw_stack_overflow(void) { pts_throw_new(&pts_type_builtin_RangeError, "Maximum call stack size exceeded"); }

#if defined(__x86_64__)
/*
 * Where pts_stack_check jumps, from anywhere in a function: before or after its prologue,
 * so the stack's alignment is unknown. It has no unwind information, so a stack trace
 * taken from here stops at it.
 */
__asm__(".text\n"
        ".globl pts_stack_overflow_stub\n"
        ".type pts_stack_overflow_stub, @function\n"
        "pts_stack_overflow_stub:\n"
        "  andq $-16, %rsp\n"
        "  call pts_throw_stack_overflow\n"
        "  ud2\n"
        ".size pts_stack_overflow_stub, .-pts_stack_overflow_stub\n");
#endif

/* Recursive, so that a thread that fails while it reports an error still ends the process. */
static pthread_mutex_t exit_lock = PTHREAD_RECURSIVE_MUTEX_INITIALIZER_NP;

void pts_begin_exit(void) { pthread_mutex_lock(&exit_lock); }

void pts_panic(const char *error_name, const char *format, ...) {
  pts_begin_exit();
  fflush(stdout);
  fprintf(stderr, "Uncaught %s: ", error_name);
  va_list args;
  va_start(args, format);
  vfprintf(stderr, format, args);
  va_end(args);
  fputc('\n', stderr);
  exit(1);
}

void pts_unreachable(void) { pts_panic("Error", "internal compiler error: a function ended without returning its value"); }

void pts_throw_uninitialized(const char *name) {
  pts_throw_new(&pts_type_builtin_ReferenceError, "Cannot access '%s' before initialization", name);
}

void pts_throw_unset_field(const char *field, const char *member) {
  if (member != NULL) {
    pts_throw_new(&pts_type_builtin_TypeError, "Cannot read properties of undefined (reading '%s')", member);
  }
  pts_throw_new(&pts_type_builtin_TypeError, "The field '%s' is undefined: it is read before the constructor sets it", field);
}
