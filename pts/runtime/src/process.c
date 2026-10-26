/*
 * The intrinsics of pts:process (D-116): the program's arguments, environment, working
 * directory and exit, and writes to stdout and stderr. The module itself is ParallelScript
 * (pts/lib/process.pts).
 */
#include "pts.h"

#include <errno.h>
#include <gc.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <uv.h>
#if defined(__APPLE__)
#include <mach-o/dyld.h>
#endif

extern char **environ;

static int program_argc;
static char **program_argv;
static int exit_code; /* process.exitCode's, or 0 */

void pts_process_init(int argc, char **argv) {
  program_argc = argc;
  program_argv = argv;
}

int pts_runtime_exit(int code) {
  pts_begin_exit();
  pts_flush_output(stdout);
  return code != 0 ? code : exit_code;
}

#if defined(__APPLE__)
/* The absolute path of the running executable (Node's process.execPath) without symbolic links, as libuv's uv_exepath, or argv[0] if the system won't tell. */
static pts_string *exec_path(void) {
  char given[PATH_MAX], resolved[PATH_MAX];
  uint32_t size = sizeof given;
  if (_NSGetExecutablePath(given, &size) == 0 && realpath(given, resolved) != NULL) {
    return pts_string_from_utf8_lossy(resolved, strlen(resolved));
  }
  return pts_string_from_cstr(program_argc > 0 ? program_argv[0] : "");
}
#else
/* The absolute path of the running executable (Node's process.execPath), or argv[0] if the system won't tell. */
static pts_string *exec_path(void) {
  for (size_t size = 256;; size *= 2) {
    char *buffer = malloc(size);
    if (buffer == NULL) {
      break;
    }
    ssize_t length = readlink("/proc/self/exe", buffer, size);
    if (length >= 0 && (size_t)length < size) {
      pts_string *path = pts_string_from_utf8_lossy(buffer, (size_t)length);
      free(buffer);
      return path;
    }
    free(buffer);
    if (length < 0) {
      break;
    }
  }
  return pts_string_from_cstr(program_argc > 0 ? program_argv[0] : "");
}
#endif

pts_string *pts_process_exec_path(void) { return exec_path(); }

pts_array *pts_process_argv(void) {
  pts_array *argv = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  pts_string *path = exec_path();
  pts_array_push(argv, &path, 1);
  pts_array_push(argv, &path, 1);
  for (int i = 1; i < program_argc; i++) {
    pts_string *argument = pts_string_from_utf8_lossy(program_argv[i], strlen(program_argv[i]));
    pts_array_push(argv, &argument, 1);
  }
  pts_publish();
  return argv;
}

pts_array *pts_process_environ(void) {
  pts_array *entries = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  for (char **entry = environ; entry != NULL && *entry != NULL; entry++) {
    pts_string *string = pts_string_from_utf8_lossy(*entry, strlen(*entry));
    pts_array_push(entries, &string, 1);
  }
  pts_publish();
  return entries;
}

double pts_process_pid(void) { return (double)getpid(); }

pts_array *pts_process_memory_usage(void) {
  size_t rss = 0;
  if (uv_resident_set_memory(&rss) != 0) {
    rss = 0;
  }
  GC_word heap_size, free_bytes;
  GC_get_heap_usage_safe(&heap_size, &free_bytes, NULL, NULL, NULL);
  double figures[] = {(double)rss, (double)heap_size, (double)(heap_size - free_bytes)};
  pts_array *usage = pts_array_of(&pts_array_types[PTS_FIELD_NUMBER], figures, 3);
  pts_publish();
  return usage;
}

pts_string *pts_process_arch(void) {
#if defined(__aarch64__)
  return pts_string_from_cstr("arm64");
#elif defined(__x86_64__)
  return pts_string_from_cstr("x64");
#else
#error "pts_process_arch: a processor ParallelScript doesn't target"
#endif
}

pts_string *pts_process_platform(void) {
#if defined(__APPLE__)
  return pts_string_from_cstr("darwin");
#elif defined(__linux__)
  return pts_string_from_cstr("linux");
#else
#error "pts_process_platform: a system ParallelScript doesn't target"
#endif
}

pts_string *pts_process_cwd(void) {
  for (size_t size = 256;; size *= 2) {
    char *buffer = malloc(size);
    if (buffer == NULL) {
      pts_panic("Error", "out of memory");
    }
    if (getcwd(buffer, size) != NULL) {
      pts_string *cwd = pts_string_from_utf8_lossy(buffer, strlen(buffer));
      free(buffer);
      return cwd;
    }
    int error = errno;
    free(buffer);
    if (error != ERANGE) {
      pts_throw_new(&pts_type_builtin_Error, "%s: %s, uv_cwd", error == ENOENT ? "ENOENT" : "EIO", strerror(error));
    }
  }
}

_Noreturn void pts_process_exit(double code) {
  pts_begin_exit();
  pts_flush_output(stdout);
  pts_flush_output(stderr);
  exit((int)code);
}

void pts_process_set_exit_code(double code) { exit_code = (int)code; }

void pts_process_write(double fd, pts_string *chunk) {
  FILE *stream = fd == 2 ? stderr : stdout;
  errno = 0;
  pts_string_write_for_output(chunk, stream);
  pts_check_output(stream);
}
