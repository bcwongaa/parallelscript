#include "pts.h"

#include <gc_gcj.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

/* The collector runs once the program has allocated as much as the heap holds (D-060), as
 * Go's default GOGC=100 does; bdwgc's own default (3) collects three times as often. The
 * GC_FREE_SPACE_DIVISOR environment variable still overrides it. */
enum { FREE_SPACE_DIVISOR = 1 };

void pts_runtime_init(int argc, char **argv) {
  (void)argc;
  (void)argv;
  GC_INIT();
  if (getenv("GC_FREE_SPACE_DIVISOR") == NULL) {
    GC_set_free_space_divisor(FREE_SPACE_DIVISOR);
  }
  GC_allow_register_threads();
  GC_init_gcj_malloc(GC_GCJ_RESERVED_MARK_PROC_INDEX, NULL); /* objects: see pts_object_new */
}

int pts_runtime_exit(int code) {
  fflush(stdout);
  return code;
}

void pts_panic(const char *error_name, const char *format, ...) {
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
