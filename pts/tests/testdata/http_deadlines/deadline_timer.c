/* Drive an early timer callback without depending on the host's clock alignment. */
#include "../../../runtime/src/http.c"

static void require(bool condition, const char *message) {
  if (!condition) {
    fprintf(stderr, "deadline assertion: %s\n", message);
    exit(1);
  }
}

int main(int argc, char **argv) {
  enum { budget_ms = 60000 };
  setenv("PTS_THREADS", "1", 1);
  pts_runtime_init(argc, argv);
  server s = {0};
  listener l = {.server = &s};
  connection *c = checked(calloc(1, sizeof *c));
  uv_tcp_init(pts_uv_loop(), &c->tcp);
  c->listener = &l;
  c->roots = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *c->roots));
  add_to_table(c);
  l.connections = c;

  start_deadline(c, 0);
  require(c->deadline == NULL && c->deadline_at == 0, "disabled timer was allocated");
  start_deadline(c, budget_ms);
  uint64_t deadline_at = c->deadline_at;
  uv_timer_stop(c->deadline); /* libuv removes a one-shot timer before delivering it */
  on_deadline(c->deadline);
  require(!c->closing, "an early callback closed the connection before its absolute deadline");
  require(c->deadline_at == deadline_at, "rearming moved the absolute deadline");
  require(uv_is_active((uv_handle_t *)c->deadline), "an early callback did not rearm the timer");
  require(uv_timer_get_due_in(c->deadline) > 0 && uv_timer_get_due_in(c->deadline) <= budget_ms,
          "remaining nanoseconds were not rounded up to a bounded millisecond wait");

  uv_timer_t *timer = c->deadline;
  uint32_t slot = c->slot;
  c->deadline_at = uv_hrtime() - 1;
  on_deadline(timer);
  require(c->closing && c->deadline == NULL && c->deadline_at == 0, "expiry did not close and detach");
  require(table.connections[slot] == NULL, "expired connection retained its id");
  require(timer->data == NULL && uv_is_closing((uv_handle_t *)timer), "timer retained the closed connection");
  uv_run(pts_uv_loop(), UV_RUN_NOWAIT);
  puts("absolute deadline timer controls passed");
  return 0;
}
