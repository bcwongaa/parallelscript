/*
 * The intrinsics of pts:fs (D-117), over libuv's synchronous file system calls, which
 * Node's synchronous functions also make, so results (readdir's order, statx's birth
 * times) and errors are Node's. The module itself is ParallelScript (pts/lib/fs.pts).
 *
 * The intrinsics that return data throw Node's error themselves (pts_throw_uv_error);
 * those that return a number return 0 or libuv's negative errno, for the module to decide.
 * Paths are the strings' own bytes: they are NUL-terminated, and WTF-8 is what the system
 * gets for a path, as in Node.
 */
#include "pts.h"

#include <stdlib.h>
#include <string.h>
#include <uv.h>

static const char *path_of(const pts_string *path) { return path->bytes; }

/* ---------------------------------------------------------------- reading and writing */

/* Closes fd, ignoring errors as Node does after a failed read or write. */
static void close_quietly(uv_file fd) {
  uv_fs_t req;
  uv_fs_close(NULL, &req, fd, NULL);
  uv_fs_req_cleanup(&req);
}

pts_string *pts_fs_read_file(pts_string *path, double flags) {
  uv_fs_t req;
  int fd = uv_fs_open(NULL, &req, path_of(path), (int)flags, 0666, NULL);
  uv_fs_req_cleanup(&req);
  if (fd < 0) {
    pts_throw_uv_error(fd, "open", path, NULL);
  }
  /* Read until end of file rather than trusting the size: files in /proc report 0. */
  size_t capacity = 64 * 1024, length = 0;
  char *bytes = malloc(capacity);
  if (bytes == NULL) {
    close_quietly(fd);
    pts_panic("RangeError", "Out of memory reading a file");
  }
  for (;;) {
    if (length == capacity) {
      capacity *= 2;
      char *grown = realloc(bytes, capacity);
      if (grown == NULL) {
        free(bytes);
        close_quietly(fd);
        pts_panic("RangeError", "Out of memory reading a file");
      }
      bytes = grown;
    }
    uv_buf_t buffer = uv_buf_init(bytes + length, (unsigned)(capacity - length));
    int n = uv_fs_read(NULL, &req, fd, &buffer, 1, -1, NULL);
    uv_fs_req_cleanup(&req);
    if (n < 0) {
      free(bytes);
      close_quietly(fd);
      pts_throw_uv_error(n, "read", NULL, NULL);
    }
    if (n == 0) {
      break;
    }
    length += (size_t)n;
  }
  close_quietly(fd);
  pts_string *text = pts_string_from_utf8_lossy(bytes, length);
  free(bytes);
  return text;
}

void pts_fs_write_file(pts_string *path, pts_string *data, double flags, double mode) {
  uv_fs_t req;
  int fd = uv_fs_open(NULL, &req, path_of(path), (int)flags, (int)mode, NULL);
  uv_fs_req_cleanup(&req);
  if (fd < 0) {
    pts_throw_uv_error(fd, "open", path, NULL);
  }
  size_t length;
  const char *bytes = pts_string_to_utf8(data, &length);
  for (size_t written = 0; written < length;) {
    uv_buf_t buffer = uv_buf_init((char *)bytes + written, (unsigned)(length - written > (1u << 30) ? 1u << 30 : length - written));
    int n = uv_fs_write(NULL, &req, fd, &buffer, 1, -1, NULL);
    uv_fs_req_cleanup(&req);
    if (n < 0) {
      close_quietly(fd);
      pts_throw_uv_error(n, "write", NULL, NULL);
    }
    written += (size_t)n;
  }
  close_quietly(fd);
}

/* ---------------------------------------------------------------- files and directories */

double pts_fs_access(pts_string *path, double mode) {
  uv_fs_t req;
  int result = uv_fs_access(NULL, &req, path_of(path), (int)mode, NULL);
  uv_fs_req_cleanup(&req);
  return result;
}

/*
 * stat (follow true) or lstat into stats, Node's order: dev, mode, nlink, uid, gid, rdev,
 * blksize, ino, size, blocks, then seconds and nanoseconds of atime, mtime, ctime and
 * birthtime. Returns 0 or the negative errno (stats is left alone).
 */
double pts_fs_stat(pts_string *path, bool follow, pts_array *stats) {
  uv_fs_t req;
  int result = follow ? uv_fs_stat(NULL, &req, path_of(path), NULL) : uv_fs_lstat(NULL, &req, path_of(path), NULL);
  if (result == 0) {
    const uv_stat_t *s = &req.statbuf;
    double values[] = {
        (double)s->st_dev,          (double)s->st_mode,          (double)s->st_nlink,         (double)s->st_uid,
        (double)s->st_gid,          (double)s->st_rdev,          (double)s->st_blksize,       (double)s->st_ino,
        (double)s->st_size,         (double)s->st_blocks,        (double)s->st_atim.tv_sec,   (double)s->st_atim.tv_nsec,
        (double)s->st_mtim.tv_sec,  (double)s->st_mtim.tv_nsec,  (double)s->st_ctim.tv_sec,   (double)s->st_ctim.tv_nsec,
        (double)s->st_birthtim.tv_sec, (double)s->st_birthtim.tv_nsec,
    };
    pts_array_push(stats, values, sizeof values / sizeof values[0]);
  }
  uv_fs_req_cleanup(&req);
  return result;
}

double pts_fs_mkdir(pts_string *path, double mode) {
  uv_fs_t req;
  int result = uv_fs_mkdir(NULL, &req, path_of(path), (int)mode, NULL);
  uv_fs_req_cleanup(&req);
  return result;
}

/* The names in directory path, sorted as libuv sorts them (Node's order); their dirent types (UV_DIRENT_*) go to types. */
pts_array *pts_fs_scandir(pts_string *path, pts_array *types) {
  uv_fs_t req;
  int result = uv_fs_scandir(NULL, &req, path_of(path), 0, NULL);
  if (result < 0) {
    uv_fs_req_cleanup(&req);
    pts_throw_uv_error(result, "scandir", path, NULL);
  }
  pts_array *names = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  uv_dirent_t entry;
  while (uv_fs_scandir_next(&req, &entry) != UV_EOF) {
    pts_string *name = pts_string_from_utf8_lossy(entry.name, strlen(entry.name));
    double type = entry.type;
    pts_array_push(names, &name, 1);
    pts_array_push(types, &type, 1);
  }
  uv_fs_req_cleanup(&req);
  return names;
}

double pts_fs_unlink(pts_string *path) {
  uv_fs_t req;
  int result = uv_fs_unlink(NULL, &req, path_of(path), NULL);
  uv_fs_req_cleanup(&req);
  return result;
}

double pts_fs_rmdir(pts_string *path) {
  uv_fs_t req;
  int result = uv_fs_rmdir(NULL, &req, path_of(path), NULL);
  uv_fs_req_cleanup(&req);
  return result;
}

double pts_fs_rename(pts_string *from, pts_string *to) {
  uv_fs_t req;
  int result = uv_fs_rename(NULL, &req, path_of(from), path_of(to), NULL);
  uv_fs_req_cleanup(&req);
  return result;
}

double pts_fs_copy_file(pts_string *source, pts_string *destination, double mode) {
  uv_fs_t req;
  int result = uv_fs_copyfile(NULL, &req, path_of(source), path_of(destination), (int)mode, NULL);
  uv_fs_req_cleanup(&req);
  return result;
}

double pts_fs_symlink(pts_string *target, pts_string *path, double flags) {
  uv_fs_t req;
  int result = uv_fs_symlink(NULL, &req, path_of(target), path_of(path), (int)flags, NULL);
  uv_fs_req_cleanup(&req);
  return result;
}

double pts_fs_chmod(pts_string *path, double mode) {
  uv_fs_t req;
  int result = uv_fs_chmod(NULL, &req, path_of(path), (int)mode, NULL);
  uv_fs_req_cleanup(&req);
  return result;
}

pts_string *pts_fs_readlink(pts_string *path) {
  uv_fs_t req;
  int result = uv_fs_readlink(NULL, &req, path_of(path), NULL);
  if (result < 0) {
    uv_fs_req_cleanup(&req);
    pts_throw_uv_error(result, "readlink", path, NULL);
  }
  pts_string *target = pts_string_from_utf8_lossy(req.ptr, strlen(req.ptr));
  uv_fs_req_cleanup(&req);
  return target;
}

/* mkdtemp(prefix + "XXXXXX"): the directory it made. */
pts_string *pts_fs_mkdtemp(pts_string *prefix) {
  size_t length = prefix->byte_len;
  char *template = malloc(length + 7);
  if (template == NULL) {
    pts_panic("RangeError", "Out of memory");
  }
  memcpy(template, prefix->bytes, length);
  memcpy(template + length, "XXXXXX", 7);
  uv_fs_t req;
  int result = uv_fs_mkdtemp(NULL, &req, template, NULL);
  if (result < 0) {
    pts_string *pattern = pts_string_from_utf8(template, length + 6);
    free(template);
    uv_fs_req_cleanup(&req);
    pts_throw_uv_error(result, "mkdtemp", pattern, NULL);
  }
  free(template);
  pts_string *made = pts_string_from_utf8_lossy(req.path, strlen(req.path));
  uv_fs_req_cleanup(&req);
  return made;
}

/* Node's error of a failed call (its errno, from an intrinsic that returned it); path and dest may be empty. */
pts_error *pts_fs_error(double error, pts_string *syscall, pts_string *path, pts_string *dest) {
  char name[64];
  size_t length = syscall->byte_len < sizeof name - 1 ? syscall->byte_len : sizeof name - 1;
  memcpy(name, syscall->bytes, length);
  name[length] = '\0';
  return (pts_error *)pts_value_as_object(pts_uv_error_new((int)error, name, path, dest));
}

/* Node's error for rm of a directory without recursive: "Path is a directory: rm returned EISDIR (is a directory) path", code ERR_FS_EISDIR. */
pts_error *pts_fs_rm_directory_error(pts_string *path) {
  static const char prefix[] = "Path is a directory: rm returned EISDIR (is a directory) ";
  size_t length = sizeof prefix - 1 + path->byte_len;
  char *text = malloc(length);
  if (text == NULL) {
    pts_panic("RangeError", "Out of memory");
  }
  memcpy(text, prefix, sizeof prefix - 1);
  memcpy(text + sizeof prefix - 1, path->bytes, path->byte_len);
  pts_string *message = pts_string_from_utf8(text, length);
  free(text);
  pts_system_error *error = (pts_system_error *)pts_value_as_object(pts_system_error_new(UV_EISDIR, message, "rm", path, NULL));
  error->errno_value = pts_value_from_number(-UV_EISDIR);
  error->code = pts_value_from_string(pts_string_from_cstr("ERR_FS_EISDIR"));
  return (pts_error *)error;
}
