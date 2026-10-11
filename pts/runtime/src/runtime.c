#include "pts.h"

#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

void pts_runtime_init(int argc, char **argv) {
  (void)argc;
  (void)argv;
  GC_INIT();
  GC_allow_register_threads();
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
