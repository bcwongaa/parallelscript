/*
 * The intrinsics of pts:process (D-116): the program's arguments, environment, working
 * directory and exit, and writes to stdout and stderr. The module itself is ParallelScript
 * (pts/lib/process.pts).
 */
#include "pts.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

extern char **environ;

static int program_argc;
static char **program_argv;
static int exit_code; /* process.exitCode's, or 0 */

void pts_process_init(int argc, char **argv) {
  program_argc = argc;
  program_argv = argv;
}

int pts_runtime_exit(int code) {
  fflush(stdout);
  return code != 0 ? code : exit_code;
}

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
  return argv;
}

pts_array *pts_process_environ(void) {
  pts_array *entries = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  for (char **entry = environ; entry != NULL && *entry != NULL; entry++) {
    pts_string *string = pts_string_from_utf8_lossy(*entry, strlen(*entry));
    pts_array_push(entries, &string, 1);
  }
  return entries;
}

double pts_process_pid(void) { return (double)getpid(); }

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
  fflush(stdout);
  fflush(stderr);
  exit((int)code);
}

void pts_process_set_exit_code(double code) { exit_code = (int)code; }

void pts_process_write(double fd, pts_string *chunk) {
  pts_string_write_for_output(chunk, fd == 2 ? stderr : stdout);
}
