/* H36 review probe. No runtime sources are modified.
 *
 * This Linux surrogate compiles the ACTUAL Apple-specific branches of http.c after
 * preloading Linux headers. The real runtime supplies scheduler threads, inboxes,
 * GC and libuv. Only http.c's posting calls are intercepted, to pause the caller
 * immediately before its last start_listening message. An earlier worker then
 * accepts three real TCP connections and hands the third to the not-yet-started
 * listener. This is a legal scheduling delay, not native-Mac test evidence.
 *
 * startup: exit 1 if an unstopped server drops this pre-start connection.
 * stopped: hold the same adoption until stop(true)+collection; exit 0 only if it
 *          closes the pending duplicate and leaves no active connection.
 */
#include "pts.h"
#include "http_internal.h"
#include "scheduler_internal.h"
#include "tls_internal.h"
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>
#include <uv.h>

static void probe_post_call(uint16_t loop, void (*call)(void *), void *data);
#if !defined(__APPLE__)
#define __APPLE__ 1
#define PROBE_UNDEFINE_APPLE
#endif
#define pts_post_call probe_post_call
#include "http.c"
#undef pts_post_call
#ifdef PROBE_UNDEFINE_APPLE
#undef __APPLE__
#endif

static atomic_bool gated;
static atomic_bool reached;
static atomic_bool processed;
static atomic_bool kept;
static atomic_bool descriptor_open;
static atomic_bool was_stopped;
static atomic_bool was_listening;
static bool stopped_mode;
static handed *delayed;
static int clients[3] = {-1, -1, -1};

static void fail(const char *operation) {
  perror(operation);
  _Exit(2);
}

static void await_flag(atomic_bool *flag, const char *description) {
  const struct timespec interval = {.tv_nsec = 1000000};
  for (unsigned attempt = 0; attempt < 10000; attempt++) {
    if (atomic_load(flag)) return;
    nanosleep(&interval, NULL);
  }
  fprintf(stderr, "timed out waiting for %s\n", description);
  _Exit(2);
}

static void observe_adopt(void *data) {
  handed *h = data;
  listener *l = h->to;
  int fd = h->fd;
  atomic_store(&was_stopped, atomic_load(&l->server->stopped));
  atomic_store(&was_listening, l->tcp != NULL);
  adopt(h);
  atomic_store(&kept, l->connections != NULL);
  atomic_store(&descriptor_open, fcntl(fd, F_GETFD) != -1);
  atomic_store(&processed, true);
}

static void connect_three(listener *l) {
  struct sockaddr_in address = {.sin_family = AF_INET, .sin_port = htons(l->server->port)};
  if (inet_pton(AF_INET, "127.0.0.1", &address.sin_addr) != 1) fail("inet_pton");
  for (unsigned i = 0; i < 3; i++) {
    clients[i] = socket(AF_INET, SOCK_STREAM, 0);
    if (clients[i] < 0 || connect(clients[i], (struct sockaddr *)&address, sizeof address) != 0) fail("connect");
  }
}

static void probe_post_call(uint16_t loop, void (*call)(void *), void *data) {
  if (call == start_listening && loop == 2) {
    atomic_store(&gated, true);
    connect_three(data);
    await_flag(stopped_mode ? &reached : &processed, "handoff before last startup post");
    /* This is still pts_http_listen's caller, which has not yet posted loop 2's
     * startup. Forward it now, after the handoff has already reached loop 2. */
  } else if (call == adopt && loop == 2 && atomic_load(&gated)) {
    if (stopped_mode) {
      delayed = data;
      atomic_store(&reached, true);
      return;
    }
    pts_post_call(loop, observe_adopt, data);
    return;
  }
  pts_post_call(loop, call, data);
}

int main(int argc, char **argv) {
  stopped_mode = argc > 1 && strcmp(argv[1], "stopped") == 0;
  if (setenv("PTS_THREADS", "3", 1) != 0) fail("setenv");
  pts_runtime_init(argc, argv);
  pts_array *tls = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  double id = pts_http_listen(0, pts_string_from_cstr("127.0.0.1"), true, tls, NULL, NULL);
  if (stopped_mode) {
    pts_http_stop(id, true);
    /* A queued malloc envelope has only an interior server pointer. This tests
     * that collection/stop cannot invalidate that server/listener storage. */
    GC_gcollect();
    pts_post_call(2, observe_adopt, delayed);
    await_flag(&processed, "post-stop adoption");
  }
  printf("mode=%s before: listening=%d stopped=%d; after: connection_kept=%d duplicate_open=%d\n",
         stopped_mode ? "stopped" : "startup", atomic_load(&was_listening), atomic_load(&was_stopped),
         atomic_load(&kept), atomic_load(&descriptor_open));
  pts_http_stop(id, true);
  for (unsigned i = 0; i < 3; i++) close(clients[i]);
  pts_run_loop();
  if (stopped_mode) {
    return atomic_load(&was_stopped) && !atomic_load(&kept) && !atomic_load(&descriptor_open) ? 0 : 1;
  }
  return !atomic_load(&was_stopped) && atomic_load(&kept) && atomic_load(&descriptor_open) ? 0 : 1;
}
