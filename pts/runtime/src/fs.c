/*
 * The intrinsics of pts:fs and pts:fs/promises (D-117, D-118), over libuv's file system
 * calls, which Node's functions also make, so results (readdir's order, statx's birth
 * times) and errors are Node's. The modules themselves are ParallelScript (pts/lib).
 *
 * Each call is a job: run makes the system calls into malloc'd memory (it may run on
 * libuv's threadpool, so it neither allocates from the collector nor throws), finish
 * makes the result, or Node's error, on the program's thread. A synchronous intrinsic
 * runs both at once and throws the error; an asynchronous one runs the job on the
 * threadpool and settles a promise with what finish made, then runs the microtasks, as
 * after every callback of the event loop (D-114).
 *
 * Intrinsics that return data report failure with Node's error; those that return a
 * number return 0 or libuv's negative errno, for the module to decide.
 */
#include "pts.h"

#include <gc.h>
#include <stdlib.h>
#include <string.h>
#include <uv.h>

typedef enum op {
  READ_FILE,
  WRITE_FILE,
  ACCESS,
  STAT,
  LSTAT,
  MKDIR,
  SCANDIR,
  UNLINK,
  RMDIR,
  RENAME,
  COPY_FILE,
  SYMLINK,
  CHMOD,
  READLINK,
  MKDTEMP,
  REALPATH,
} op;

enum { STAT_COUNT = 18 };

typedef struct job {
  uv_work_t work; /* libuv's request while the job runs on the threadpool */
  op op;
  char *path, *dest; /* malloc'd copies, NUL-terminated; dest may be NULL */
  size_t path_len, dest_len;
  char *data; /* the bytes to write, malloc'd */
  size_t data_len;
  int flags, mode; /* open's; mode is also access's mode, copyfile's and symlink's flags */
  bool as_bytes;   /* a read's result is a Uint8Array, not text (D-165) */
  /* What run found. */
  int result;          /* 0 or more, or libuv's negative errno */
  const char *syscall; /* the call that failed */
  bool names_path;     /* whether its error names path (and dest): a read's or write's doesn't */
  char *bytes;         /* what was read, a link's target, a path made: malloc'd */
  size_t length;
  double stats[STAT_COUNT];
  char **names; /* scandir's entries and their UV_DIRENT_* types, malloc'd */
  unsigned char *types;
  size_t count;
} job;

static void *checked(void *memory) {
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory in a file system call");
  }
  return memory;
}

static char *copy_bytes(const char *bytes, size_t length) {
  char *copy = checked(malloc(length + 1));
  memcpy(copy, bytes, length);
  copy[length] = '\0';
  return copy;
}

/* A job of op on path (and dest), owning copies of them. */
static job *new_job(op op, const pts_string *path, const pts_string *dest) {
  job *j = checked(calloc(1, sizeof *j));
  j->op = op;
  j->path = copy_bytes(path->bytes, path->byte_len);
  j->path_len = path->byte_len;
  if (dest != NULL) {
    j->dest = copy_bytes(dest->bytes, dest->byte_len);
    j->dest_len = dest->byte_len;
  }
  return j;
}

static void free_job(job *j) {
  for (size_t i = 0; i < j->count; i++) {
    free(j->names[i]);
  }
  free(j->names);
  free(j->types);
  free(j->bytes);
  free(j->data);
  free(j->dest);
  free(j->path);
  free(j);
}

/* ---------------------------------------------------------------- the system calls */

static void fail(job *j, int error, const char *syscall, bool names_path) {
  j->result = error;
  j->syscall = syscall;
  j->names_path = names_path;
}

/* Closes fd, ignoring errors as Node does after a failed read or write. */
static void close_quietly(uv_file fd) {
  uv_fs_t req;
  uv_fs_close(NULL, &req, fd, NULL);
  uv_fs_req_cleanup(&req);
}

static void read_file(job *j) {
  uv_fs_t req;
  int fd = uv_fs_open(NULL, &req, j->path, j->flags, 0666, NULL);
  uv_fs_req_cleanup(&req);
  if (fd < 0) {
    fail(j, fd, "open", true);
    return;
  }
  /* Read until end of file rather than trusting the size: files in /proc report 0. */
  size_t capacity = 64 * 1024;
  j->bytes = checked(malloc(capacity));
  for (;;) {
    if (j->length == capacity) {
      capacity *= 2;
      j->bytes = checked(realloc(j->bytes, capacity));
    }
    uv_buf_t buffer = uv_buf_init(j->bytes + j->length, (unsigned)(capacity - j->length));
    int n = uv_fs_read(NULL, &req, fd, &buffer, 1, -1, NULL);
    uv_fs_req_cleanup(&req);
    if (n < 0) {
      fail(j, n, "read", false);
      break;
    }
    if (n == 0) {
      break;
    }
    j->length += (size_t)n;
  }
  close_quietly(fd);
}

static void write_file(job *j) {
  uv_fs_t req;
  int fd = uv_fs_open(NULL, &req, j->path, j->flags, j->mode, NULL);
  uv_fs_req_cleanup(&req);
  if (fd < 0) {
    fail(j, fd, "open", true);
    return;
  }
  for (size_t written = 0; written < j->data_len;) {
    size_t chunk = j->data_len - written > (1u << 30) ? 1u << 30 : j->data_len - written;
    uv_buf_t buffer = uv_buf_init(j->data + written, (unsigned)chunk);
    int n = uv_fs_write(NULL, &req, fd, &buffer, 1, -1, NULL);
    uv_fs_req_cleanup(&req);
    if (n < 0) {
      fail(j, n, "write", false);
      break;
    }
    written += (size_t)n;
  }
  close_quietly(fd);
}

/* Node's order: dev, mode, nlink, uid, gid, rdev, blksize, ino, size, blocks, then the seconds and nanoseconds of atime, mtime, ctime, birthtime. */
static void stat_file(job *j, bool follow) {
  uv_fs_t req;
  j->result = follow ? uv_fs_stat(NULL, &req, j->path, NULL) : uv_fs_lstat(NULL, &req, j->path, NULL);
  if (j->result == 0) {
    const uv_stat_t *s = &req.statbuf;
    double values[STAT_COUNT] = {
        (double)s->st_dev,           (double)s->st_mode,          (double)s->st_nlink,        (double)s->st_uid,
        (double)s->st_gid,           (double)s->st_rdev,          (double)s->st_blksize,      (double)s->st_ino,
        (double)s->st_size,          (double)s->st_blocks,        (double)s->st_atim.tv_sec,  (double)s->st_atim.tv_nsec,
        (double)s->st_mtim.tv_sec,   (double)s->st_mtim.tv_nsec,  (double)s->st_ctim.tv_sec,  (double)s->st_ctim.tv_nsec,
        (double)s->st_birthtim.tv_sec, (double)s->st_birthtim.tv_nsec,
    };
    memcpy(j->stats, values, sizeof values);
  }
  uv_fs_req_cleanup(&req);
}

static void scan_directory(job *j) {
  uv_fs_t req;
  int result = uv_fs_scandir(NULL, &req, j->path, 0, NULL);
  if (result < 0) {
    uv_fs_req_cleanup(&req);
    fail(j, result, "scandir", true);
    return;
  }
  j->names = checked(malloc(((size_t)result + 1) * sizeof *j->names));
  j->types = checked(malloc((size_t)result + 1));
  uv_dirent_t entry;
  while (uv_fs_scandir_next(&req, &entry) != UV_EOF) {
    j->names[j->count] = copy_bytes(entry.name, strlen(entry.name));
    j->types[j->count] = (unsigned char)entry.type;
    j->count++;
  }
  uv_fs_req_cleanup(&req);
}

/* readlink, mkdtemp and realpath: the path the call gives, or its error. */
static void path_result(job *j, int result, const char *syscall, const char *made) {
  if (result < 0) {
    fail(j, result, syscall, true);
    return;
  }
  j->length = strlen(made);
  j->bytes = copy_bytes(made, j->length);
}

static void run(job *j) {
  uv_fs_t req;
  switch (j->op) {
  case READ_FILE:
    read_file(j);
    return;
  case WRITE_FILE:
    write_file(j);
    return;
  case STAT:
  case LSTAT:
    stat_file(j, j->op == STAT);
    return;
  case SCANDIR:
    scan_directory(j);
    return;
  case ACCESS:
    j->result = uv_fs_access(NULL, &req, j->path, j->mode, NULL);
    break;
  case MKDIR:
    j->result = uv_fs_mkdir(NULL, &req, j->path, j->mode, NULL);
    break;
  case UNLINK:
    j->result = uv_fs_unlink(NULL, &req, j->path, NULL);
    break;
  case RMDIR:
    j->result = uv_fs_rmdir(NULL, &req, j->path, NULL);
    break;
  case RENAME:
    j->result = uv_fs_rename(NULL, &req, j->path, j->dest, NULL);
    break;
  case COPY_FILE:
    j->result = uv_fs_copyfile(NULL, &req, j->path, j->dest, j->mode, NULL);
    break;
  case SYMLINK:
    j->result = uv_fs_symlink(NULL, &req, j->path, j->dest, j->mode, NULL);
    break;
  case CHMOD:
    j->result = uv_fs_chmod(NULL, &req, j->path, j->mode, NULL);
    break;
  case READLINK: {
    int result = uv_fs_readlink(NULL, &req, j->path, NULL);
    path_result(j, result, "readlink", result < 0 ? NULL : req.ptr);
    break;
  }
  case MKDTEMP: {
    int result = uv_fs_mkdtemp(NULL, &req, j->path, NULL);
    path_result(j, result, "mkdtemp", result < 0 ? NULL : req.path);
    break;
  }
  case REALPATH: {
    int result = uv_fs_realpath(NULL, &req, j->path, NULL);
    path_result(j, result, "realpath", result < 0 ? NULL : req.ptr);
    break;
  }
  }
  uv_fs_req_cleanup(&req);
}

/* ---------------------------------------------------------------- results */

static pts_string *string_of(const char *bytes, size_t length) { return pts_string_from_utf8_lossy(bytes, length); }

/*
 * The job's result as a value, or false and Node's error in *result; scandir's types and
 * stat's numbers go to out. The job is freed.
 */
static bool finish(job *j, pts_array *out, pts_value *result) {
  bool ok = true;
  bool reports = j->op == READ_FILE || j->op == WRITE_FILE || j->op == SCANDIR || j->op == READLINK || j->op == MKDTEMP || j->op == REALPATH;
  if (j->result < 0 && reports) {
    pts_string *path = j->names_path ? pts_string_from_utf8(j->path, j->path_len) : NULL;
    *result = pts_uv_error_new(j->result, j->syscall, path, NULL);
    ok = false;
  } else {
    switch (j->op) {
    case READ_FILE:
      if (j->as_bytes) {
        pts_typed_array *bytes = pts_typed_array_new(&pts_type_builtin_Uint8Array, (double)j->length);
        if (j->length > 0) {
          memcpy(bytes->data, j->bytes, j->length); /* a new array: no other thread sees it yet */
        }
        *result = pts_value_from_object(bytes);
        break;
      }
      *result = pts_value_from_string(string_of(j->bytes, j->length));
      break;
    case READLINK:
    case MKDTEMP:
    case REALPATH:
      *result = pts_value_from_string(string_of(j->bytes, j->length));
      break;
    case WRITE_FILE:
      *result = PTS_VALUE_UNDEFINED;
      break;
    case SCANDIR: {
      pts_array *names = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
      for (size_t i = 0; i < j->count; i++) {
        pts_string *name = string_of(j->names[i], strlen(j->names[i]));
        double type = j->types[i];
        pts_array_push(names, &name, 1);
        pts_array_push(out, &type, 1);
      }
      *result = pts_value_from_object(names);
      break;
    }
    case STAT:
    case LSTAT:
      if (j->result == 0) {
        pts_array_push(out, j->stats, STAT_COUNT);
      }
      *result = pts_value_from_number(j->result);
      break;
    default:
      *result = pts_value_from_number(j->result);
      break;
    }
  }
  free_job(j);
  return ok;
}

/* Runs a job now: its result, or Node's error thrown. */
static pts_value run_now(job *j, pts_array *out) {
  run(j);
  pts_value result;
  if (!finish(j, out, &result)) {
    pts_throw(result);
  }
  return result;
}

/*
 * A job on the threadpool: its promise, and the array finish writes to. The job is in
 * malloc'd memory, which the collector doesn't scan, so this is uncollectable (a root)
 * until the job is done.
 */
typedef struct pending {
  pts_value promise;
  pts_array *out;
} pending;

static void work(uv_work_t *request) { run((job *)request); }

static void done(uv_work_t *request, int status) {
  (void)status; /* jobs aren't cancelled */
  job *j = (job *)request;
  pending *p = j->work.data;
  pts_value promise = p->promise, result;
  bool ok = finish(j, p->out, &result);
  GC_FREE(p);
  pts_promise_settle(promise, ok, result);
  pts_run_microtasks();
}

/* Runs a job on the threadpool: the promise its result settles. */
static pts_value start(job *j, pts_array *out) {
  pending *p = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *p));
  p->promise = pts_promise_pending();
  p->out = out;
  j->work.data = p;
  uv_queue_work(pts_uv_loop(), &j->work, work, done);
  return p->promise;
}

/* ---------------------------------------------------------------- the intrinsics */

static job *read_job(pts_string *path, double flags, bool as_bytes) {
  job *j = new_job(READ_FILE, path, NULL);
  j->flags = (int)flags;
  j->as_bytes = as_bytes;
  return j;
}

static job *write_job(pts_string *path, pts_string *data, double flags, double mode) {
  job *j = new_job(WRITE_FILE, path, NULL);
  size_t length;
  const char *bytes = pts_string_to_utf8(data, &length);
  j->data = copy_bytes(bytes, length);
  j->data_len = length;
  j->flags = (int)flags;
  j->mode = (int)mode;
  return j;
}

/* A write of a typed array's bytes, copied as other threads may be writing them (D-143). */
static job *bytes_write_job(pts_string *path, const pts_typed_array *data, double flags, double mode) {
  job *j = new_job(WRITE_FILE, path, NULL);
  size_t unit = pts_field_kind_size(data->type->element), length = data->length * unit;
  j->data = checked(malloc(length > 0 ? length : 1));
  pts_copy_shared(j->data, data->data, length, unit);
  j->data_len = length;
  j->flags = (int)flags;
  j->mode = (int)mode;
  return j;
}

static job *call_job(op op, pts_string *path, pts_string *dest, double mode) {
  job *j = new_job(op, path, dest != NULL && dest->byte_len > 0 ? dest : NULL);
  j->mode = (int)mode;
  return j;
}

/* mkdtemp(prefix + "XXXXXX"). */
static job *mkdtemp_job(pts_string *prefix) {
  return new_job(MKDTEMP, pts_string_concat(prefix, pts_string_from_cstr("XXXXXX")), NULL);
}

pts_string *pts_fs_read_file(pts_string *path, double flags) { return pts_value_as_string(run_now(read_job(path, flags, false), NULL)); }

pts_typed_array *pts_fs_read_file_bytes(pts_string *path, double flags) {
  return (pts_typed_array *)pts_value_as_object(run_now(read_job(path, flags, true), NULL));
}

void pts_fs_write_file(pts_string *path, pts_string *data, double flags, double mode) { run_now(write_job(path, data, flags, mode), NULL); }

void pts_fs_write_file_bytes(pts_string *path, pts_typed_array *data, double flags, double mode) {
  run_now(bytes_write_job(path, data, flags, mode), NULL);
}

double pts_fs_access(pts_string *path, double mode) { return pts_value_as_number(run_now(call_job(ACCESS, path, NULL, mode), NULL)); }

double pts_fs_stat(pts_string *path, bool follow, pts_array *stats) {
  return pts_value_as_number(run_now(call_job(follow ? STAT : LSTAT, path, NULL, 0), stats));
}

double pts_fs_mkdir(pts_string *path, double mode) { return pts_value_as_number(run_now(call_job(MKDIR, path, NULL, mode), NULL)); }

pts_array *pts_fs_scandir(pts_string *path, pts_array *types) {
  return (pts_array *)pts_value_as_object(run_now(call_job(SCANDIR, path, NULL, 0), types));
}

double pts_fs_unlink(pts_string *path) { return pts_value_as_number(run_now(call_job(UNLINK, path, NULL, 0), NULL)); }

double pts_fs_rmdir(pts_string *path) { return pts_value_as_number(run_now(call_job(RMDIR, path, NULL, 0), NULL)); }

double pts_fs_rename(pts_string *from, pts_string *to) { return pts_value_as_number(run_now(call_job(RENAME, from, to, 0), NULL)); }

double pts_fs_copy_file(pts_string *source, pts_string *destination, double mode) {
  return pts_value_as_number(run_now(call_job(COPY_FILE, source, destination, mode), NULL));
}

double pts_fs_symlink(pts_string *target, pts_string *path, double flags) {
  return pts_value_as_number(run_now(call_job(SYMLINK, target, path, flags), NULL));
}

double pts_fs_chmod(pts_string *path, double mode) { return pts_value_as_number(run_now(call_job(CHMOD, path, NULL, mode), NULL)); }

pts_string *pts_fs_readlink(pts_string *path) { return pts_value_as_string(run_now(call_job(READLINK, path, NULL, 0), NULL)); }

pts_string *pts_fs_mkdtemp(pts_string *prefix) { return pts_value_as_string(run_now(mkdtemp_job(prefix), NULL)); }

pts_value pts_fs_read_file_async(pts_string *path, double flags) { return start(read_job(path, flags, false), NULL); }

pts_value pts_fs_read_file_bytes_async(pts_string *path, double flags) { return start(read_job(path, flags, true), NULL); }

pts_value pts_fs_write_file_async(pts_string *path, pts_string *data, double flags, double mode) {
  return start(write_job(path, data, flags, mode), NULL);
}

pts_value pts_fs_write_file_bytes_async(pts_string *path, pts_typed_array *data, double flags, double mode) {
  return start(bytes_write_job(path, data, flags, mode), NULL);
}

/* The calls that resolve with 0 or the negative errno, by name: "access", "mkdir", … */
pts_value pts_fs_call_async(pts_string *call, pts_string *path, pts_string *dest, double mode) {
  static const struct {
    const char *name;
    op op;
  } calls[] = {
      {"access", ACCESS}, {"mkdir", MKDIR},          {"unlink", UNLINK},   {"rmdir", RMDIR},
      {"rename", RENAME}, {"copyfile", COPY_FILE}, {"symlink", SYMLINK}, {"chmod", CHMOD},
  };
  for (size_t i = 0; i < sizeof calls / sizeof calls[0]; i++) {
    if (strlen(calls[i].name) == call->byte_len && memcmp(calls[i].name, call->bytes, call->byte_len) == 0) {
      return start(call_job(calls[i].op, path, dest, mode), NULL);
    }
  }
  pts_panic("Error", "internal error: no file system call %.*s", (int)call->byte_len, call->bytes);
}

pts_value pts_fs_stat_async(pts_string *path, bool follow, pts_array *stats) { return start(call_job(follow ? STAT : LSTAT, path, NULL, 0), stats); }

pts_value pts_fs_scandir_async(pts_string *path, pts_array *types) { return start(call_job(SCANDIR, path, NULL, 0), types); }

pts_value pts_fs_readlink_async(pts_string *path) { return start(call_job(READLINK, path, NULL, 0), NULL); }

pts_value pts_fs_mkdtemp_async(pts_string *prefix) { return start(mkdtemp_job(prefix), NULL); }

pts_value pts_fs_realpath_async(pts_string *path) { return start(call_job(REALPATH, path, NULL, 0), NULL); }

/* ---------------------------------------------------------------- errors */

/* Node's error of a call that returned the errno error; path and dest may be empty. */
pts_error *pts_fs_error(double error, pts_string *syscall, pts_string *path, pts_string *dest) {
  char name[64];
  size_t length = syscall->byte_len < sizeof name - 1 ? syscall->byte_len : sizeof name - 1;
  memcpy(name, syscall->bytes, length);
  name[length] = '\0';
  return (pts_error *)pts_value_as_object(pts_uv_error_new((int)error, name, path, dest));
}

/* Node's error for rm of a directory without recursive: "Path is a directory: rm returned EISDIR (is a directory) path", code ERR_FS_EISDIR. */
pts_error *pts_fs_rm_directory_error(pts_string *path) {
  PTS_STATIC_STRING(prefix, PTS_STRING_ASCII, 57, 57, "Path is a directory: rm returned EISDIR (is a directory) ");
  pts_string *message = pts_string_concat(prefix, path);
  pts_system_error *error = (pts_system_error *)pts_value_as_object(pts_system_error_new(UV_EISDIR, message, "rm", path, NULL));
  error->errno_value = pts_value_from_number(-UV_EISDIR);
  error->code = pts_value_from_string(pts_string_from_cstr("ERR_FS_EISDIR"));
  return (pts_error *)error;
}
