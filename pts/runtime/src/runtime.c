#include "pts.h"

#include <gc_gcj.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

void pts_runtime_init(int argc, char **argv) {
  (void)argc;
  (void)argv;
  GC_INIT();
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
  pts_panic("ReferenceError", "Cannot access '%s' before initialization", name);
}

void pts_throw_error(pts_string *name, pts_string *message) { pts_panic(name->bytes, "%s", message->bytes); }
