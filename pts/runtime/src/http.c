/*
 * pts:http's server (D-136): HTTP/1.1 over libuv's TCP on the loop that listens, or with
 * reusePort on every loop (D-137), each accepting on a socket of its own bound to the same
 * port with SO_REUSEPORT, so the kernel spreads connections across them (on macOS, whose
 * SO_REUSEPORT doesn't, each on a descriptor of one socket, handing the connections it
 * accepts to the loops in turn). Each connection
 * stays on the loop that accepted it: it reads requests one at a time (pipelined ones wait
 * in its buffer), hands each to the program's request callback with an id, writes the
 * response once pts_http_respond answers that id, and goes on with the next request or
 * closes, as keep-alive says. Ids name a loop, a slot in that loop's table and the slot's
 * generation, so an answer for a connection that has closed meanwhile finds nothing and
 * is dropped.
 *
 * While the program has a request, the connection goes on reading (up to MAX_HEAD
 * bytes of what follows), so that it sees the client leave: then it closes at once and
 * calls the disconnect callback with the Request the request callback returned, which
 * aborts its signal (D-163). Responses wait in libuv's write queue: past MAX_QUEUED
 * bytes, the connection reads and processes nothing more until the queue drains, so a
 * client that doesn't read can't make the server queue response after response.
 *
 * An https server (D-224) does the same inside TLS: each connection's session (tls.c) takes
 * the bytes that come, goes on with the handshake and then decrypts the requests into the
 * connection's buffer, and encrypts what the server writes. Each listener has its own
 * identity (the certificate chain and key), made by pts_http_listen, so that loops don't
 * share a key's state. A response that closes the connection, and stop()'s closing of an
 * idle one, end with close_notify.
 *
 * libuv's handles, the bytes read and written and TLS's sessions and identities are
 * malloc'd: they hold no collected pointers. A server is uncollectable (never freed: a
 * program makes few), so the collector sees its callbacks; a connection's Request is in an
 * uncollectable cell of its own.
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

enum {
  MAX_HEAD = 64 * 1024,          /* the request line and headers: more is 431 */
  MAX_BODY = PTS_HTTP_MAX_BODY,  /* a request body: more is 413 */
  READ_SIZE = 64 * 1024,         /* bytes asked of each read */
  SLOT_BITS = 20,                /* an id is (generation * 2^LOOP_BITS + loop) * 2^SLOT_BITS + slot, */
  LOOP_BITS = 8,                 /* which is at most 2^52, so a double holds it exactly */
  GENERATION_MASK = (1 << 24) - 1,
  BACKLOG = 511,
  MAX_SERVERS = 1024,
  MAX_QUEUED = 1024 * 1024, /* bytes of responses waiting to be written, past which a connection stops taking requests */
};

/* A server's socket on one loop, and the connections that loop accepted from it; only that loop touches it once it listens. */
typedef struct listener {
  uv_tcp_t *tcp; /* NULL until the loop listens, and once it stops */
  struct server *server;
  struct connection *connections;
  pts_tls_identity *identity; /* an https server's certificate and key for this loop's connections; NULL for http */
  int fd;                     /* the bound socket, until the loop takes it over */
  uint16_t loop;
} listener;

typedef struct server {
  pts_closure *on_request;    /* (id, method, target, headers, body) => unknown: the Request */
  pts_closure *on_disconnect; /* (request: unknown) => void */
  atomic_bool stopped;
  atomic_bool close_active; /* stop(true): connections with requests in flight close too */
  int port;
  uint32_t listener_count;
  uint32_t handshake_timeout, headers_timeout; /* absolute millisecond budgets; 0 disables (D-230) */
  atomic_uint turn; /* on macOS, where the loops share a socket: the listener the next connection goes to (hand_off) */
  listener listeners[]; /* the calling loop's first */
} server;

/* What the collector must see of a connection: the Request the program is handling, or PTS_VALUE_ABSENT. */
typedef struct connection_roots {
  pts_value request;
} connection_roots;

typedef struct connection {
  uv_tcp_t tcp;
  listener *listener;
  struct connection *next, *prev; /* the listener's connections */
  char *in;                       /* bytes read and not yet taken by a request */
  size_t in_len, in_cap;
  connection_roots *roots; /* uncollectable */
  pts_tls *tls;            /* an https connection's session; NULL for http */
  uv_timer_t *deadline;    /* separately closed/freed: the TCP close callback frees this connection */
  uint64_t deadline_at;    /* monotonic nanoseconds; 0 while no deadline applies */
  bool headers_pending;   /* false once headers are complete, including while buffering a body */
  uint32_t slot;
  bool busy;        /* a request is with the program, or its last response is being written */
  bool processing;  /* inside process(): an answer given meanwhile leaves the next request to its loop */
  bool draining;    /* more than MAX_QUEUED bytes wait to be written: nothing more is read or processed */
  bool keep_alive;  /* of the request being answered */
  bool http10;      /* the request being answered is HTTP/1.0, which keeps alive only if asked */
  bool head;        /* the request being answered is a HEAD */
  bool continued;   /* 100 Continue was sent for the request being read */
  bool secured;     /* an https connection's handshake is complete */
  bool closing;
} connection;

/* The loop's connections by slot, and each slot's generation; free slots chain through `free_next`. */
typedef struct connection_table {
  connection **connections;
  uint32_t *generations, *free_next;
  uint32_t capacity, free_head;
} connection_table;

static _Thread_local connection_table table = {.free_head = UINT32_MAX};
static server *servers[MAX_SERVERS]; /* a server's id is its index; they are never freed (a program makes few) */
static uint32_t server_count;
static uv_mutex_t servers_lock;
static uv_once_t servers_once = UV_ONCE_INIT;

static void *checked(void *memory) {
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory in the HTTP server");
  }
  return memory;
}

/* ---------------------------------------------------------------- ids */

static double id_of(const connection *c) {
  return ((double)table.generations[c->slot] * (1 << LOOP_BITS) + pts_current_loop) * (1 << SLOT_BITS) + c->slot;
}

static void add_to_table(connection *c) {
  if (table.free_head == UINT32_MAX) {
    uint32_t old = table.capacity, capacity = old == 0 ? 64 : old * 2;
    if (capacity > (1u << SLOT_BITS)) {
      pts_panic("RangeError", "Too many HTTP connections on one loop");
    }
    table.connections = checked(realloc(table.connections, capacity * sizeof *table.connections));
    table.generations = checked(realloc(table.generations, capacity * sizeof *table.generations));
    table.free_next = checked(realloc(table.free_next, capacity * sizeof *table.free_next));
    for (uint32_t s = old; s < capacity; s++) {
      table.connections[s] = NULL;
      table.generations[s] = 0;
      table.free_next[s] = s + 1 < capacity ? s + 1 : UINT32_MAX;
    }
    table.free_head = old;
    table.capacity = capacity;
  }
  c->slot = table.free_head;
  table.free_head = table.free_next[c->slot];
  table.connections[c->slot] = c;
}

static void remove_from_table(const connection *c) {
  table.connections[c->slot] = NULL;
  table.generations[c->slot] = (table.generations[c->slot] + 1) & GENERATION_MASK;
  table.free_next[c->slot] = table.free_head;
  table.free_head = c->slot;
}

/* The connection id names, if it is still open and on this loop. */
static connection *connection_of(double id) {
  if (!(id >= 0) || id != (double)(uint64_t)id) {
    return NULL;
  }
  uint64_t n = (uint64_t)id;
  uint32_t slot = (uint32_t)(n & ((1u << SLOT_BITS) - 1)), loop = (uint32_t)(n >> SLOT_BITS) & ((1u << LOOP_BITS) - 1);
  uint32_t generation = (uint32_t)(n >> (SLOT_BITS + LOOP_BITS));
  if (loop != pts_current_loop || slot >= table.capacity || table.connections[slot] == NULL || table.generations[slot] != generation) {
    return NULL;
  }
  return table.connections[slot];
}

/* ---------------------------------------------------------------- connections */

static void on_closed(uv_handle_t *handle) {
  connection *c = (connection *)handle;
  GC_FREE(c->roots);
  if (c->tls != NULL) {
    pts_tls_free(c->tls);
  }
  free(c->in);
  free(c);
}

static void stop_deadline(connection *c) {
  if (c->deadline != NULL) {
    uv_timer_stop(c->deadline);
  }
  c->deadline_at = 0;
}

static void on_deadline_closed(uv_handle_t *handle) { free(handle); }

static void close_connection(connection *c) {
  if (c->closing) {
    return;
  }
  c->closing = true;
  stop_deadline(c);
  if (c->deadline != NULL) {
    c->deadline->data = NULL;
    uv_close((uv_handle_t *)c->deadline, on_deadline_closed);
    c->deadline = NULL;
  }
  remove_from_table(c);
  if (c->prev != NULL) {
    c->prev->next = c->next;
  } else {
    c->listener->connections = c->next;
  }
  if (c->next != NULL) {
    c->next->prev = c->prev;
  }
  uv_close((uv_handle_t *)&c->tcp, on_closed);
}

/* The budget is absolute: neither more bytes nor a late read callback can refresh it. */
static bool deadline_expired(connection *c) {
  if (c->deadline_at != 0 && uv_hrtime() >= c->deadline_at) {
    close_connection(c);
    return true;
  }
  return false;
}

static void on_deadline(uv_timer_t *timer) {
  connection *c = timer->data;
  uint64_t now = uv_hrtime();
  if (now < c->deadline_at) {
    /* libuv's millisecond clock can round down; preserve the absolute nanosecond budget. */
    uint64_t remaining = (c->deadline_at - now + 999999) / 1000000;
    uv_update_time(pts_uv_loop());
    uv_timer_start(timer, on_deadline, remaining, 0);
    return;
  }
  close_connection(c); /* no queued 408 or TLS alert: an unread answer must not defer expiry */
}

static void start_deadline(connection *c, uint32_t milliseconds) {
  stop_deadline(c);
  if (milliseconds == 0) {
    return;
  }
  if (c->deadline == NULL) {
    c->deadline = checked(malloc(sizeof *c->deadline));
    uv_timer_init(pts_uv_loop(), c->deadline);
    c->deadline->data = c;
  }
  c->deadline_at = uv_hrtime() + (uint64_t)milliseconds * 1000000;
  uv_update_time(pts_uv_loop()); /* a long callback must not arm against the loop's old time */
  uv_timer_start(c->deadline, on_deadline, milliseconds, 0);
}

typedef struct write_request {
  uv_write_t req;
  connection *connection;
  char *bytes; /* malloc'd, the request's */
  bool close_after;
} write_request;

static void on_alloc(uv_handle_t *handle, size_t suggested, uv_buf_t *buf);
static void on_alloc_sealed(uv_handle_t *handle, size_t suggested, uv_buf_t *buf);
static void on_read(uv_stream_t *stream, ssize_t nread, const uv_buf_t *buf);
static void process(connection *c);
static void send_last(connection *c);

static size_t queued(connection *c) { return uv_stream_get_write_queue_size((uv_stream_t *)&c->tcp); }

/* Idle keepalive is not a header read; buffered followers wait for preceding output. */
static void next_header_deadline(connection *c) {
  if (!c->closing && !c->busy && c->headers_pending && c->in_len > 0 &&
      (c->tls == NULL || c->secured) && c->deadline_at == 0 && queued(c) == 0) {
    start_deadline(c, c->listener->server->headers_timeout);
  }
}

/* Reads what the client sends: an https connection's bytes go to its session first. */
static void start_reading(connection *c) { uv_read_start((uv_stream_t *)&c->tcp, c->tls != NULL ? on_alloc_sealed : on_alloc, on_read); }

static void on_written(uv_write_t *req, int status) {
  write_request *w = (write_request *)req;
  connection *c = w->connection;
  if (!c->closing && (status < 0 || w->close_after)) {
    close_connection(c);
  } else if (!c->closing && c->draining && queued(c) <= MAX_QUEUED / 2) {
    c->draining = false; /* the client is reading again: on with the requests it sent */
    start_reading(c);
    process(c);
    pts_run_microtasks();
  }
  next_header_deadline(c); /* also when the queue never reached MAX_QUEUED */
  free(w->bytes);
  free(w);
}

/* Writes len bytes (malloc'd, now the write's) and, if close_after, closes the connection once they are out. */
static void write_out(connection *c, char *bytes, size_t len, bool close_after) {
  write_request *w = checked(malloc(sizeof *w));
  w->connection = c;
  w->bytes = bytes;
  w->close_after = close_after;
  uv_buf_t buf = uv_buf_init(bytes, (unsigned)len);
  if (uv_write(&w->req, (uv_stream_t *)&c->tcp, &buf, 1, on_written) < 0) {
    free(bytes);
    free(w);
    close_connection(c);
  }
}

/* Sends what an https connection's session has for the client and, if close_after, closes the connection after it. */
static void send_sealed(connection *c, bool close_after) {
  char *bytes;
  size_t len;
  if (pts_tls_take_output(c->tls, &bytes, &len)) {
    write_out(c, bytes, len, close_after);
  } else if (close_after) {
    close_connection(c);
  }
}

/* Encrypts len bytes for an https connection's client, then close_notify if close_after: false if the session has failed. */
static bool seal(connection *c, const char *bytes, size_t len, bool close_after) {
  return (len == 0 || pts_tls_write(c->tls, bytes, len) == PTS_TLS_DONE) && (!close_after || pts_tls_close(c->tls) == PTS_TLS_DONE);
}

/*
 * Writes the bytes of head and then of body (encrypted on an https connection) and, if
 * close_after, closes the connection once they are out.
 */
static void send_parts(connection *c, const char *head, size_t head_len, const char *body, size_t body_len, bool close_after) {
  if (c->tls != NULL) {
    if (!seal(c, head, head_len, false) || !seal(c, body, body_len, close_after)) {
      send_last(c); /* the session failed: its alert, and nothing after it */
      close_connection(c);
      return;
    }
    send_sealed(c, close_after);
    return;
  }
  char *bytes = checked(malloc(head_len + body_len));
  memcpy(bytes, head, head_len);
  if (body_len > 0) {
    memcpy(bytes + head_len, body, body_len);
  }
  write_out(c, bytes, head_len + body_len, close_after);
}

static void send_bytes(connection *c, const char *text, size_t len, bool close_after) { send_parts(c, text, len, NULL, 0, close_after); }

/* "date: <now as HTTP-date>\r\n", formatted once a second per loop (RFC 9110 asks origin servers for a date). */
static const char *date_line(void) {
  static _Thread_local char line[64];
  static _Thread_local time_t formatted = -1;
  time_t now = time(NULL);
  if (now != formatted) {
    struct tm utc;
    gmtime_r(&now, &utc);
    strftime(line, sizeof line, "date: %a, %d %b %Y %H:%M:%S GMT\r\n", &utc);
    formatted = now;
  }
  return line;
}

/* An error the server answers itself, closing the connection after it. */
static void send_error(connection *c, int status, const char *reason) {
  char text[192];
  int len = snprintf(text, sizeof text, "HTTP/1.1 %d %s\r\n%scontent-length: 0\r\nconnection: close\r\n\r\n", status, reason, date_line());
  stop_deadline(c);
  c->busy = true; /* nothing more is read from it */
  uv_read_stop((uv_stream_t *)&c->tcp);
  send_bytes(c, text, (size_t)len, true);
}

/* ---------------------------------------------------------------- parsing */

/* A parsed request's parts, pointing into the connection's buffer. */
typedef struct request {
  const char *method, *target;
  size_t method_len, target_len;
  int minor; /* HTTP/1.minor */
  const char *head_end;   /* past the blank line */
  pts_array *headers;     /* names (lowercase) and values, in turn */
  bool chunked, keep_alive, expect_continue;
  int64_t content_length; /* -1 if not given; lengths above MAX_BODY capped at MAX_BODY + 1 */
} request;

static bool is_token_char(char ch) {
  return ch > ' ' && ch < 127 && strchr("()<>@,;:\\\"/[]?={}", ch) == NULL;
}

static pts_string *lowercase(const char *bytes, size_t len) {
  char *lower = checked(malloc(len + 1));
  for (size_t i = 0; i < len; i++) {
    lower[i] = (bytes[i] >= 'A' && bytes[i] <= 'Z') ? (char)(bytes[i] + 32) : bytes[i];
  }
  pts_string *s = pts_string_from_utf8(lower, len);
  free(lower);
  return s;
}

bool pts_http_equals_ignoring_case(const char *bytes, size_t len, const char *word) {
  return strlen(word) == len && strncasecmp(bytes, word, len) == 0;
}

bool pts_http_lists(const char *value, size_t len, const char *word) {
  size_t i = 0;
  while (i < len) {
    while (i < len && (value[i] == ' ' || value[i] == '\t' || value[i] == ',')) {
      i++;
    }
    size_t start = i;
    while (i < len && value[i] != ',') {
      i++;
    }
    size_t end = i;
    while (end > start && (value[end - 1] == ' ' || value[end - 1] == '\t')) {
      end--;
    }
    if (pts_http_equals_ignoring_case(value + start, end - start, word)) {
      return true;
    }
  }
  return false;
}

/* Parses the request line and headers ending at head_end; false for a malformed one. */
static bool parse_head(const char *bytes, const char *head_end, request *r) {
  const char *p = bytes, *line_end = memchr(p, '\n', (size_t)(head_end - p));
  const char *space = memchr(p, ' ', (size_t)(line_end - p));
  if (space == NULL || space == p) {
    return false;
  }
  r->method = p, r->method_len = (size_t)(space - p);
  for (size_t i = 0; i < r->method_len; i++) {
    if (!is_token_char(p[i])) {
      return false;
    }
  }
  r->target = space + 1;
  const char *second = memchr(r->target, ' ', (size_t)(line_end - r->target));
  if (second == NULL || second == r->target) {
    return false;
  }
  r->target_len = (size_t)(second - r->target);
  const char *version = second + 1;
  size_t version_len = (size_t)(line_end - version) - (line_end[-1] == '\r');
  if (version_len != 8 || memcmp(version, "HTTP/1.", 7) != 0 || version[7] < '0' || version[7] > '9') {
    return false;
  }
  r->minor = version[7] - '0';
  r->keep_alive = r->minor >= 1;
  r->content_length = -1;
  r->headers = pts_array_of(&pts_array_types[PTS_FIELD_STRING], NULL, 0);
  for (p = line_end + 1; p < head_end;) {
    const char *end = memchr(p, '\n', (size_t)(head_end - p));
    size_t len = (size_t)(end - p) - (end > p && end[-1] == '\r');
    if (len == 0) {
      break; /* the blank line */
    }
    const char *colon = memchr(p, ':', len);
    if (colon == NULL || colon == p) {
      return false;
    }
    size_t name_len = (size_t)(colon - p);
    for (size_t i = 0; i < name_len; i++) {
      if (!is_token_char(p[i])) {
        return false;
      }
    }
    const char *value = colon + 1, *value_end = p + len;
    while (value < value_end && (*value == ' ' || *value == '\t')) {
      value++;
    }
    while (value_end > value && (value_end[-1] == ' ' || value_end[-1] == '\t')) {
      value_end--;
    }
    size_t value_len = (size_t)(value_end - value);
    if (memchr(value, '\0', value_len) != NULL || memchr(value, '\r', value_len) != NULL) {
      return false; /* RFC 9112 asks to reject these; pts:http's Headers take the rest as they are */
    }
    if (pts_http_equals_ignoring_case(p, name_len, "content-length")) {
      if (value_len == 0 || r->content_length >= 0) {
        return false;
      }
      int64_t length = 0;
      for (size_t i = 0; i < value_len; i++) {
        if (value[i] < '0' || value[i] > '9') {
          return false;
        }
        /* Keep checking syntax after saturation: an oversized length with junk is still 400, not 413. */
        if (length <= MAX_BODY) {
          length = length * 10 + (value[i] - '0');
          if (length > MAX_BODY) {
            length = MAX_BODY + 1;
          }
        }
      }
      r->content_length = length;
    } else if (pts_http_equals_ignoring_case(p, name_len, "transfer-encoding")) {
      if (r->chunked || !pts_http_equals_ignoring_case(value, value_len, "chunked")) {
        return false; /* Only one coding is supported, not a chain or repeated field. */
      }
      r->chunked = true;
    } else if (pts_http_equals_ignoring_case(p, name_len, "connection")) {
      if (pts_http_lists(value, value_len, "close")) {
        r->keep_alive = false;
      } else if (pts_http_lists(value, value_len, "keep-alive")) {
        r->keep_alive = true;
      }
    } else if (pts_http_equals_ignoring_case(p, name_len, "expect")) {
      r->expect_continue = pts_http_equals_ignoring_case(value, value_len, "100-continue");
    }
    pts_string *pair[] = {lowercase(p, name_len), pts_string_from_utf8_lossy(value, value_len)};
    pts_array_push(r->headers, pair, 2);
    p = end + 1;
  }
  return !(r->chunked && r->content_length >= 0); /* No ambiguous boundary, in either field order, even with Content-Length: 0. */
}

int pts_http_dechunk(const char *body, const char *end, char **out, size_t *out_len, size_t *used) {
  const char *p = body;
  size_t len = 0, cap = 0;
  char *bytes = NULL;
  for (;;) {
    const char *line_end = memchr(p, '\n', (size_t)(end - p));
    if (line_end == NULL) {
      free(bytes);
      return (size_t)(end - p) > 1024 ? -1 : 0;
    }
    char *size_end;
    unsigned long long size = strtoull(p, &size_end, 16);
    if (size_end == p) {
      free(bytes);
      return -1;
    }
    if (size > MAX_BODY || len + size > MAX_BODY) {
      free(bytes);
      return -2;
    }
    p = line_end + 1;
    if (size == 0) {
      for (;;) { /* trailers, until the blank line */
        const char *trailer_end = memchr(p, '\n', (size_t)(end - p));
        if (trailer_end == NULL) {
          free(bytes);
          return 0;
        }
        bool blank = trailer_end == p || (trailer_end == p + 1 && *p == '\r');
        p = trailer_end + 1;
        if (blank) {
          break;
        }
      }
      *out = bytes, *out_len = len, *used = (size_t)(p - body);
      return 1;
    }
    if ((size_t)(end - p) < size + 1) {
      free(bytes);
      return 0;
    }
    if (len + size > cap) {
      cap = (len + size) * 2;
      bytes = checked(realloc(bytes, cap));
    }
    memcpy(bytes + len, p, size);
    len += size;
    p += size;
    if (*p == '\r') {
      p++;
    }
    if (p >= end) {
      free(bytes);
      return 0;
    }
    if (*p != '\n') {
      free(bytes);
      return -1;
    }
    p++;
  }
}

/* ---------------------------------------------------------------- requests */

static void dispatch(connection *c, request *r, const char *body, size_t body_len) {
  c->busy = true;
  c->keep_alive = r->keep_alive && !atomic_load(&c->listener->server->stopped);
  c->http10 = r->minor == 0;
  c->head = pts_http_equals_ignoring_case(r->method, r->method_len, "HEAD");
  c->continued = false;
  pts_closure *on_request = c->listener->server->on_request;
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) != 0) {
    pts_report_uncaught(pts_caught(&handler));
  }
  c->roots->request = ((pts_value(*)(pts_closure *, double, pts_string *, pts_string *, pts_array *, pts_string *))on_request->code)(
      on_request, id_of(c), pts_string_from_utf8(r->method, r->method_len), pts_string_from_utf8_lossy(r->target, r->target_len), r->headers,
      pts_string_from_utf8_lossy(body, body_len));
  pts_try_leave(&handler);
  pts_run_microtasks();
}

/*
 * The client left while the program had its request: closes the connection now (a late
 * answer finds nothing) and calls the disconnect callback, which aborts the request's
 * signal. The microtasks it queues are the caller's to run.
 */
static void disconnected(connection *c) {
  pts_value request = c->roots->request;
  c->roots->request = PTS_VALUE_ABSENT;
  close_connection(c);
  pts_closure *on_disconnect = c->listener->server->on_disconnect;
  pts_handler handler;
  pts_try_enter(&handler);
  if (_setjmp(handler.jump) != 0) {
    pts_report_uncaught(pts_caught(&handler));
  }
  ((void (*)(pts_closure *, pts_value))on_disconnect->code)(on_disconnect, request);
  pts_try_leave(&handler);
}

/* Takes count bytes from the front of the connection's buffer. */
static void consume(connection *c, size_t count) {
  memmove(c->in, c->in + count, c->in_len - count);
  c->in_len -= count;
}

/* Hands the program the next complete requests in the buffer, one at a time, while it isn't busy with one. */
static void process(connection *c) {
  if (c->processing) {
    return; /* an answer given inside this loop's own dispatch: the loop goes on */
  }
  c->processing = true;
  while (!c->busy && !c->closing && !c->draining) {
    next_header_deadline(c);
    if (deadline_expired(c)) {
      break;
    }
    size_t blank = 0;
    while (blank < c->in_len && (c->in[blank] == '\r' || c->in[blank] == '\n')) {
      blank++; /* empty lines before a request */
    }
    consume(c, blank);
    const char *end = c->in + c->in_len, *head_end = NULL;
    for (const char *p = c->in; p < end; p++) {
      if (*p == '\n' && ((p > c->in && p[-1] == '\n') || (p > c->in + 1 && p[-1] == '\r' && p[-2] == '\n'))) {
        head_end = p + 1;
        break;
      }
    }
    if (head_end == NULL) {
      if (c->in_len > MAX_HEAD) {
        send_error(c, 431, "Request Header Fields Too Large");
      }
      break;
    }
    c->headers_pending = false;
    stop_deadline(c); /* the body, 100 Continue and the handler are not header reads */
    request r = {0};
    if (!parse_head(c->in, head_end, &r)) {
      send_error(c, 400, "Bad Request");
      break;
    }
    char *body = NULL;
    size_t body_len = 0, used = (size_t)(head_end - c->in);
    if (r.chunked) {
      size_t chunked_used;
      int done = pts_http_dechunk(head_end, end, &body, &body_len, &chunked_used);
      if (done < 0) {
        send_error(c, done == -2 ? 413 : 400, done == -2 ? "Content Too Large" : "Bad Request");
        break;
      }
      if (done == 0) {
        if (r.expect_continue && !c->continued) {
          c->continued = true;
          send_bytes(c, "HTTP/1.1 100 Continue\r\n\r\n", 25, false);
        }
        break;
      }
      used += chunked_used;
    } else if (r.content_length > 0) {
      if (r.content_length > MAX_BODY) {
        send_error(c, 413, "Content Too Large");
        break;
      }
      if ((int64_t)(end - head_end) < r.content_length) {
        if (r.expect_continue && !c->continued) {
          c->continued = true;
          send_bytes(c, "HTTP/1.1 100 Continue\r\n\r\n", 25, false);
        }
        break;
      }
      body_len = (size_t)r.content_length;
      body = checked(malloc(body_len));
      memcpy(body, head_end, body_len);
      used += body_len;
    }
    request copy = r; /* r's method and target point into the buffer: copied before consume moves its bytes */
    char *line = checked(malloc(r.method_len + r.target_len));
    memcpy(line, r.method, r.method_len);
    memcpy(line + r.method_len, r.target, r.target_len);
    copy.method = line, copy.target = line + r.method_len;
    consume(c, used);
    dispatch(c, &copy, body, body_len);
    free(line);
    free(body);
  }
  c->processing = false;
}

/* Makes room for at least READ_SIZE more bytes in the connection's buffer. */
static void reserve(connection *c) {
  if (c->in_cap - c->in_len < READ_SIZE) {
    c->in_cap = c->in_len + READ_SIZE;
    c->in = checked(realloc(c->in, c->in_cap));
  }
}

static void on_alloc(uv_handle_t *handle, size_t suggested, uv_buf_t *buf) {
  (void)suggested;
  connection *c = (connection *)handle;
  reserve(c);
  *buf = uv_buf_init(c->in + c->in_len, READ_SIZE);
}

/* An https connection's bytes as they come, before its session decrypts them: libuv hands the buffer to on_read at once. */
static void on_alloc_sealed(uv_handle_t *handle, size_t suggested, uv_buf_t *buf) {
  (void)handle, (void)suggested;
  static _Thread_local char sealed[READ_SIZE];
  *buf = uv_buf_init(sealed, READ_SIZE);
}

/* The client closed the connection or it failed: closed, and a request the program has is aborted. */
static void lost(connection *c) {
  if (c->roots->request != PTS_VALUE_ABSENT) {
    disconnected(c);
    pts_run_microtasks();
  } else {
    close_connection(c);
  }
}

/*
 * Gives an https connection's session len bytes from the client: goes on with the
 * handshake, then decrypts the requests into the connection's buffer, and sends what the
 * session has for the client meanwhile (the handshake's messages). PTS_TLS_WANT while the
 * session goes on; PTS_TLS_CLOSED for the client's close_notify, PTS_TLS_FAILED for a failure.
 */
static pts_tls_status unseal(connection *c, const char *bytes, size_t len) {
  pts_tls_received(c->tls, bytes, len);
  pts_tls_status status = PTS_TLS_DONE;
  if (!c->secured) {
    status = pts_tls_handshake(c->tls);
    if (deadline_expired(c)) {
      return PTS_TLS_FAILED;
    }
    c->secured = status == PTS_TLS_DONE;
    if (c->secured) {
      start_deadline(c, c->listener->server->headers_timeout);
    }
  }
  reserve(c); /* process() reads the buffer, decrypted requests or not */
  while (status == PTS_TLS_DONE) {
    size_t got = 0;
    status = pts_tls_read(c->tls, c->in + c->in_len, c->in_cap - c->in_len, &got);
    c->in_len += got;
    reserve(c);
  }
  if (status == PTS_TLS_WANT) {
    send_sealed(c, false);
  }
  return status;
}

/*
 * Sends what an https connection's session has for the client (close_notify, an alert)
 * only if the socket takes it at once, as the connection closes, so that a client that
 * doesn't read can't hold it open.
 */
static void send_last(connection *c) {
  char *bytes;
  size_t len;
  if (queued(c) == 0 && pts_tls_take_output(c->tls, &bytes, &len)) {
    uv_buf_t buf = uv_buf_init(bytes, (unsigned)len);
    uv_try_write((uv_stream_t *)&c->tcp, &buf, 1);
    free(bytes);
  }
}

/*
 * An https connection's session has ended, after the requests that came before its end:
 * by the client's close_notify, which is answered with the server's (RFC 8446 §6.1), or by
 * a failure, after mbedTLS's alert if it wrote one. As a plain connection's end, the
 * connection closes and a request the program has is aborted.
 */
static void end_sealed(connection *c, bool notified) {
  if (notified) {
    pts_tls_close(c->tls);
  }
  send_last(c);
  lost(c);
}

static void on_read(uv_stream_t *stream, ssize_t nread, const uv_buf_t *buf) {
  connection *c = (connection *)stream;
  if (nread < 0) { /* the client closed, or an error */
    lost(c);
    return;
  }
  if (c->closing || deadline_expired(c)) {
    return;
  }
  pts_tls_status status = PTS_TLS_WANT;
  if (c->tls != NULL) {
    status = unseal(c, buf->base, (size_t)nread);
  } else {
    c->in_len += (size_t)nread;
  }
  if (c->busy && c->in_len > MAX_HEAD) {
    uv_read_stop(stream); /* pipelined requests wait in the kernel until this one is answered */
  }
  process(c);
  if (status != PTS_TLS_WANT && !c->closing) {
    end_sealed(c, status == PTS_TLS_CLOSED);
  }
}

static void start_connection(listener *l, connection *c);
#if defined(__APPLE__)
static void hand_off(listener *l, uv_stream_t *stream);
#endif

static void on_connection(uv_stream_t *stream, int status) {
  listener *l = stream->data;
  if (status < 0 || atomic_load(&l->server->stopped)) {
    return;
  }
#if defined(__APPLE__)
  if (l->server->listener_count > 1) {
    hand_off(l, stream);
    return;
  }
#endif
  connection *c = checked(calloc(1, sizeof *c));
  uv_tcp_init(stream->loop, &c->tcp);
  if (uv_accept(stream, (uv_stream_t *)&c->tcp) < 0) {
    uv_close((uv_handle_t *)&c->tcp, on_closed);
    return;
  }
  start_connection(l, c);
}

#if defined(__APPLE__)
/* A connection one loop accepted for another (hand_off): a descriptor of its own for it. */
typedef struct handed {
  listener *to;
  int fd;
} handed;

static void on_taken_closed(uv_handle_t *handle) { free(handle); }

/* On the loop a connection was handed to: it takes the connection over, unless the server stopped there meanwhile. */
static void adopt(void *data) {
  handed *h = data;
  listener *l = h->to;
  int fd = h->fd;
  free(h);
  /* The handoff can reach this loop before its listener's startup message. */
  if (atomic_load(&l->server->stopped)) {
    close(fd);
    return;
  }
  connection *c = checked(calloc(1, sizeof *c));
  uv_tcp_init(pts_uv_loop(), &c->tcp);
  if (uv_tcp_open(&c->tcp, fd) != 0) {
    close(fd);
    uv_close((uv_handle_t *)&c->tcp, on_closed);
    return;
  }
  start_connection(l, c);
}

/*
 * The loops share a socket on macOS (another_listening_socket), and the loop that is awake
 * would take every connection before the others wake, so the connections go to the loops in
 * turn, as Linux's SO_REUSEPORT spreads them: one for another loop is accepted here and its
 * descriptor's copy handed to that loop.
 */
static void hand_off(listener *l, uv_stream_t *stream) {
  server *s = l->server;
  listener *to = &s->listeners[atomic_fetch_add(&s->turn, 1) % s->listener_count];
  uv_tcp_t *taken = checked(malloc(sizeof *taken));
  uv_tcp_init(stream->loop, taken);
  uv_os_fd_t fd;
  if (uv_accept(stream, (uv_stream_t *)taken) == 0 && uv_fileno((uv_handle_t *)taken, &fd) == 0) {
    handed *h = checked(malloc(sizeof *h));
    h->to = to;
    h->fd = fcntl(fd, F_DUPFD_CLOEXEC, 0);
    if (h->fd < 0) {
      free(h);
    } else if (to == l) {
      adopt(h);
    } else {
      pts_post_call(to->loop, adopt, h);
    }
  }
  uv_close((uv_handle_t *)taken, on_taken_closed); /* closes the descriptor accept made, not its copy */
}
#endif

/* A connection the listener's loop now has: it reads requests from it, through TLS for an https server. */
static void start_connection(listener *l, connection *c) {
  uv_tcp_nodelay(&c->tcp, 1);
  c->roots = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *c->roots));
  c->listener = l;
  add_to_table(c);
  c->next = l->connections;
  if (l->connections != NULL) {
    l->connections->prev = c;
  }
  l->connections = c;
  if (l->identity != NULL) {
    pts_value failure;
    c->tls = pts_tls_server(l->identity, &failure);
    if (c->tls == NULL) {
      close_connection(c); /* mbedTLS couldn't make the session (out of memory): the client sees the connection close */
      return;
    }
  }
  c->headers_pending = true;
  start_deadline(c, c->tls != NULL ? l->server->handshake_timeout : l->server->headers_timeout);
  start_reading(c);
}

/* ---------------------------------------------------------------- responses */

static const char *reason_phrase(int status) {
  switch (status) {
  case 100: return "Continue";
  case 101: return "Switching Protocols";
  case 200: return "OK";
  case 201: return "Created";
  case 202: return "Accepted";
  case 204: return "No Content";
  case 206: return "Partial Content";
  case 301: return "Moved Permanently";
  case 302: return "Found";
  case 303: return "See Other";
  case 304: return "Not Modified";
  case 307: return "Temporary Redirect";
  case 308: return "Permanent Redirect";
  case 400: return "Bad Request";
  case 401: return "Unauthorized";
  case 403: return "Forbidden";
  case 404: return "Not Found";
  case 405: return "Method Not Allowed";
  case 406: return "Not Acceptable";
  case 408: return "Request Timeout";
  case 409: return "Conflict";
  case 410: return "Gone";
  case 411: return "Length Required";
  case 412: return "Precondition Failed";
  case 413: return "Content Too Large";
  case 414: return "URI Too Long";
  case 415: return "Unsupported Media Type";
  case 422: return "Unprocessable Content";
  case 426: return "Upgrade Required";
  case 429: return "Too Many Requests";
  case 431: return "Request Header Fields Too Large";
  case 500: return "Internal Server Error";
  case 501: return "Not Implemented";
  case 502: return "Bad Gateway";
  case 503: return "Service Unavailable";
  case 504: return "Gateway Timeout";
  default: return "";
  }
}

/* A growing buffer of the response being written (malloc'd). */
typedef struct text {
  char *bytes;
  size_t len, cap;
} text;

static void add(text *t, const char *bytes, size_t len) {
  if (t->len + len > t->cap) {
    t->cap = (t->len + len) * 2 + 256;
    t->bytes = checked(realloc(t->bytes, t->cap));
  }
  memcpy(t->bytes + t->len, bytes, len);
  t->len += len;
}

static void add_cstr(text *t, const char *s) { add(t, s, strlen(s)); }

/* Whether a header the program gave is one the server writes itself. */
static bool is_framing_header(const pts_string *name) {
  return pts_http_equals_ignoring_case(name->bytes, name->byte_len, "content-length") || pts_http_equals_ignoring_case(name->bytes, name->byte_len, "transfer-encoding") ||
         pts_http_equals_ignoring_case(name->bytes, name->byte_len, "connection");
}

void pts_http_respond(double id, double status, pts_string *status_text, pts_array *headers, pts_string *body) {
  connection *c = connection_of(id);
  if (c == NULL || !c->busy || c->closing) {
    return; /* closed meanwhile, or answered already */
  }
  int code = status >= 100 && status <= 999 ? (int)status : 500;
  bool bodiless = (code >= 100 && code < 200) || code == 204 || code == 304;
  bool keep_alive = c->keep_alive && !atomic_load(&c->listener->server->stopped);
  text t = {0};
  char line[96];
  add(&t, line, (size_t)snprintf(line, sizeof line, "HTTP/1.1 %d ", code));
  if (status_text->byte_len > 0) {
    add(&t, status_text->bytes, status_text->byte_len);
  } else {
    add_cstr(&t, reason_phrase(code));
  }
  add_cstr(&t, "\r\n");
  size_t count = pts_array_length_of(headers);
  bool dated = false;
  for (size_t i = 0; i + 1 < count; i += 2) {
    const pts_string *name = pts_value_as_string(pts_array_element(headers, (double)i));
    const pts_string *value = pts_value_as_string(pts_array_element(headers, (double)i + 1));
    if (is_framing_header(name) || memchr(name->bytes, '\n', name->byte_len) != NULL || memchr(name->bytes, '\r', name->byte_len) != NULL ||
        memchr(value->bytes, '\n', value->byte_len) != NULL || memchr(value->bytes, '\r', value->byte_len) != NULL) {
      continue; /* framed by the server; a line break would split the response */
    }
    dated = dated || pts_http_equals_ignoring_case(name->bytes, name->byte_len, "date");
    add(&t, name->bytes, name->byte_len);
    add_cstr(&t, ": ");
    add(&t, value->bytes, value->byte_len);
    add_cstr(&t, "\r\n");
  }
  if (!dated) {
    add_cstr(&t, date_line());
  }
  if (!bodiless) {
    add(&t, line, (size_t)snprintf(line, sizeof line, "content-length: %u\r\n", body->byte_len));
  }
  if (!keep_alive) {
    add_cstr(&t, "connection: close\r\n");
  } else if (c->http10) {
    add_cstr(&t, "connection: keep-alive\r\n");
  }
  add_cstr(&t, "\r\n");
  bool with_body = !bodiless && !c->head;
  send_parts(c, t.bytes, t.len, with_body ? body->bytes : NULL, with_body ? body->byte_len : 0, !keep_alive);
  free(t.bytes);
  c->roots->request = PTS_VALUE_ABSENT;
  if (!keep_alive) {
    uv_read_stop((uv_stream_t *)&c->tcp); /* busy until the response is out and the connection closes */
    return;
  }
  c->busy = false;
  c->headers_pending = true;
  if (queued(c) > MAX_QUEUED) {
    c->draining = true; /* the client doesn't read: on_written goes on once it does */
    uv_read_stop((uv_stream_t *)&c->tcp);
    return;
  }
  start_reading(c); /* reading stopped if a pipelined request filled the buffer */
  process(c);
}

/* ---------------------------------------------------------------- servers */

static void init_servers(void) { uv_mutex_init(&servers_lock); }

static void on_listener_closed(uv_handle_t *handle) { free(handle); }

/* Closes a connection that has no request: an https one after close_notify, if the client can take it at once. */
static void close_idle(connection *c) {
  if (c->tls != NULL && c->secured && pts_tls_close(c->tls) == PTS_TLS_DONE) {
    send_last(c);
  }
  close_connection(c);
}

/* The address to listen on: an IPv4 or IPv6 address ("localhost" is 127.0.0.1) and the port. */
static struct sockaddr_storage address_of(pts_string *hostname, double port) {
  char host[256];
  snprintf(host, sizeof host, "%.*s", (int)hostname->byte_len, hostname->bytes);
  if (strcmp(host, "localhost") == 0) {
    strcpy(host, "127.0.0.1");
  }
  struct sockaddr_storage address;
  if (uv_ip4_addr(host, (int)port, (struct sockaddr_in *)&address) != 0 && uv_ip6_addr(host, (int)port, (struct sockaddr_in6 *)&address) != 0) {
    pts_throw_new(&pts_type_builtin_TypeError, "Invalid hostname \"%s\": an IPv4 or IPv6 address is expected", host);
  }
  return address;
}

static socklen_t address_length(const struct sockaddr_storage *address) {
  return address->ss_family == AF_INET6 ? sizeof(struct sockaddr_in6) : sizeof(struct sockaddr_in);
}

static in_port_t *port_of(struct sockaddr_storage *address) {
  return address->ss_family == AF_INET6 ? &((struct sockaddr_in6 *)address)->sin6_port : &((struct sockaddr_in *)address)->sin_port;
}

/* A socket bound to address and listening (SO_REUSEADDR as libuv sets it, SO_REUSEPORT if asked), or a libuv error code. */
static int listening_socket(const struct sockaddr_storage *address, bool reuse_port, int *fd) {
#if defined(__APPLE__)
  *fd = socket(address->ss_family, SOCK_STREAM, 0);
  if (*fd >= 0 && (fcntl(*fd, F_SETFL, O_NONBLOCK) != 0 || fcntl(*fd, F_SETFD, FD_CLOEXEC) != 0)) {
    int error = -errno;
    close(*fd);
    return error;
  }
#else
  *fd = socket(address->ss_family, SOCK_STREAM | SOCK_NONBLOCK | SOCK_CLOEXEC, 0);
#endif
  if (*fd < 0) {
    return -errno;
  }
  int on = 1;
  if (setsockopt(*fd, SOL_SOCKET, SO_REUSEADDR, &on, sizeof on) != 0 || (reuse_port && setsockopt(*fd, SOL_SOCKET, SO_REUSEPORT, &on, sizeof on) != 0) ||
      bind(*fd, (const struct sockaddr *)address, address_length(address)) != 0 || listen(*fd, BACKLOG) != 0) {
    int error = -errno;
    close(*fd);
    return error;
  }
  return 0;
}

/* On the listener's loop: accepts connections from its socket, unless the server stopped before the loop got here. */
static void start_listening(void *data) {
  listener *l = data;
  if (atomic_load(&l->server->stopped)) {
    close(l->fd);
    return;
  }
  uv_tcp_t *tcp = checked(malloc(sizeof *tcp));
  uv_tcp_init(pts_uv_loop(), tcp);
  tcp->data = l;
  int error = uv_tcp_open(tcp, l->fd);
  if (error == 0) {
    error = uv_listen((uv_stream_t *)tcp, BACKLOG, on_connection);
  }
  if (error != 0) {
    uv_close((uv_handle_t *)tcp, on_listener_closed); /* closes the socket too: the other loops serve the port */
    return;
  }
  l->tcp = tcp;
}

/*
 * On the listener's loop: stops accepting, closes the idle connections, and the busy ones
 * after their responses; with stop(true), those whose requests are still with the program
 * at once, aborting the requests' signals.
 */
static void stop_listening(void *data) {
  listener *l = data;
  if (l->tcp != NULL) {
    uv_close((uv_handle_t *)l->tcp, on_listener_closed);
    l->tcp = NULL;
  }
  bool close_active = atomic_load(&l->server->close_active);
  for (connection *c = l->connections, *next; c != NULL; c = next) {
    next = c->next;
    if (!c->busy) {
      close_idle(c);
    } else if (close_active && c->roots->request != PTS_VALUE_ABSENT) {
      disconnected(c);
    } else {
      c->keep_alive = false;
    }
  }
}

/*
 * The socket of a listener after the first: on Linux one of its own bound to the same port
 * with SO_REUSEPORT, so the kernel spreads connections across them; macOS's SO_REUSEPORT
 * doesn't spread them, so there every listener has a descriptor of the first's socket, and
 * the loop that accepts first takes a connection.
 */
static int another_listening_socket(server *s, const struct sockaddr_storage *address, bool reuse_port, int *fd) {
#if defined(__APPLE__)
  (void)address, (void)reuse_port;
  *fd = fcntl(s->listeners[0].fd, F_DUPFD_CLOEXEC, 0);
  return *fd < 0 ? -errno : 0;
#else
  (void)s;
  return listening_socket(address, reuse_port, fd);
#endif
}

/*
 * Binds a socket per listener to the same port (the first one's, if port is 0), before any
 * loop listens, so that every error is the caller's to throw: 0, or a libuv error code
 * with no socket left open.
 */
static int bind_listeners(server *s, struct sockaddr_storage *address, bool reuse_port) {
  for (uint32_t i = 0; i < s->listener_count; i++) {
    int error = i == 0 ? listening_socket(address, reuse_port, &s->listeners[i].fd) : another_listening_socket(s, address, reuse_port, &s->listeners[i].fd);
    if (error != 0) {
      for (uint32_t j = 0; j < i; j++) {
        close(s->listeners[j].fd);
      }
      return error;
    }
    if (i == 0) {
      struct sockaddr_storage bound;
      socklen_t bound_len = sizeof bound;
      getsockname(s->listeners[0].fd, (struct sockaddr *)&bound, &bound_len);
      *port_of(address) = *port_of(&bound);
      s->port = ntohs(*port_of(&bound));
    }
  }
  return 0;
}

/* serve's tls option as http.pts passes it: the certificate's kind ("cert" or "certFile") and value, the key's, then the passphrase if given. */
enum { TLS_CERT_KIND, TLS_CERT, TLS_KEY_KIND, TLS_KEY, TLS_PASSPHRASE };

static pts_string *tls_part(pts_array *tls, int part) { return pts_value_as_string(pts_array_element(tls, part)); }

/*
 * The file at path, whole and NUL-terminated for mbedTLS, its length with the NUL in *len;
 * or NULL, with *failure Node's error, as readFileSync's, if it can't be read.
 */
static char *read_whole(pts_string *path, size_t *len, pts_value *failure) {
  char *name = checked(malloc(path->byte_len + 1));
  memcpy(name, path->bytes, path->byte_len);
  name[path->byte_len] = '\0';
  int fd = open(name, O_RDONLY | O_CLOEXEC), error = -errno;
  free(name);
  if (fd < 0) {
    *failure = pts_uv_error_new(error, "open", path, NULL);
    return NULL;
  }
  size_t used = 0, capacity = 16 * 1024;
  char *bytes = checked(malloc(capacity));
  for (;;) {
    if (used + 1 == capacity) {
      capacity *= 2;
      bytes = checked(realloc(bytes, capacity));
    }
    ssize_t n = read(fd, bytes + used, capacity - used - 1);
    if (n < 0 && errno == EINTR) {
      continue;
    }
    if (n < 0) {
      *failure = pts_uv_error_new(-errno, "read", NULL, NULL);
      close(fd);
      pts_tls_forget(bytes, used);
      return NULL;
    }
    if (n == 0) {
      break;
    }
    used += (size_t)n;
  }
  close(fd);
  bytes[used] = '\0';
  *len = used + 1;
  return bytes;
}

/*
 * A PEM option's text (value, or the file it names if kind is "certFile" or "keyFile"),
 * NUL-terminated, malloc'd, its length with the NUL in *len; or NULL, with *failure why.
 */
static char *pem_of(pts_string *kind, pts_string *value, size_t *len, pts_value *failure) {
  if (kind->byte_len > 4 && memcmp(kind->bytes + kind->byte_len - 4, "File", 4) == 0) {
    return read_whole(value, len, failure);
  }
  char *text = checked(malloc(value->byte_len + 1));
  memcpy(text, value->bytes, value->byte_len);
  text[value->byte_len] = '\0';
  *len = value->byte_len + 1;
  return text;
}

static void free_identities(server *s) {
  for (uint32_t i = 0; i < s->listener_count; i++) {
    if (s->listeners[i].identity != NULL) {
      pts_tls_identity_free(s->listeners[i].identity);
    }
  }
}

/*
 * Gives each listener of an https server an identity of its own from serve's tls option,
 * or frees the server and throws serve's Error: a file that can't be read, a certificate
 * or key mbedTLS can't take.
 */
static void make_identities(server *s, pts_array *tls) {
  size_t cert_len = 0, key_len = 0;
  pts_value failure = PTS_VALUE_ABSENT;
  char *cert = pem_of(tls_part(tls, TLS_CERT_KIND), tls_part(tls, TLS_CERT), &cert_len, &failure);
  char *key = cert != NULL ? pem_of(tls_part(tls, TLS_KEY_KIND), tls_part(tls, TLS_KEY), &key_len, &failure) : NULL;
  const pts_string *passphrase = pts_array_length_of(tls) > TLS_PASSPHRASE ? tls_part(tls, TLS_PASSPHRASE) : NULL;
  if (key != NULL) {
    s->listeners[0].identity = pts_tls_identity_new(cert, cert_len, key, key_len, passphrase, &failure);
  }
  if (s->listeners[0].identity != NULL && s->listener_count > 1) {
    /* The other loops take the first's key as DER, so that an encrypted key is decrypted once. */
    size_t der_len;
    char *der = pts_tls_identity_key(s->listeners[0].identity, &der_len);
    if (der != NULL) {
      pts_tls_forget(key, key_len);
      key = der, key_len = der_len, passphrase = NULL;
    }
  }
  for (uint32_t i = 1; i < s->listener_count && failure == PTS_VALUE_ABSENT; i++) {
    s->listeners[i].identity = pts_tls_identity_new(cert, cert_len, key, key_len, passphrase, &failure);
  }
  free(cert);
  if (key != NULL) {
    pts_tls_forget(key, key_len);
  }
  if (failure != PTS_VALUE_ABSENT) {
    free_identities(s);
    GC_FREE(s);
    pts_throw(failure);
  }
}

double pts_http_listen(double port, pts_string *hostname, bool every_loop, pts_array *tls, double handshake_timeout, double headers_timeout,
                       pts_closure *on_request, pts_closure *on_disconnect) {
  uv_once(&servers_once, init_servers);
  pts_uv_loop(); /* throws on a thread without a loop */
  struct sockaddr_storage address = address_of(hostname, port);
  uint32_t count = every_loop ? pts_start_loops() : 1;
  server *s = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *s + count * sizeof(listener)));
  s->on_request = on_request;
  s->on_disconnect = on_disconnect;
  s->listener_count = count;
  s->handshake_timeout = (uint32_t)handshake_timeout;
  s->headers_timeout = (uint32_t)headers_timeout;
  for (uint32_t i = 0; i < count; i++) { /* this thread's loop first (where start_listening opens it below), then the loops after it */
    s->listeners[i].server = s;
    s->listeners[i].loop = (uint16_t)((pts_own_loop() + i) % (every_loop ? count : 1u << LOOP_BITS));
  }
  if (pts_array_length_of(tls) > 0) {
    make_identities(s, tls);
  }
  int error = bind_listeners(s, &address, every_loop);
  if (error != 0) {
    free_identities(s);
    GC_FREE(s);
    char host[INET6_ADDRSTRLEN] = "";
    uv_ip_name((const struct sockaddr *)&address, host, sizeof host);
    pts_throw_new(&pts_type_builtin_Error, "listen %s: %s %s:%d", uv_err_name(error), uv_strerror(error), host, ntohs(*port_of(&address)));
  }
  uv_mutex_lock(&servers_lock);
  if (server_count == MAX_SERVERS) {
    uv_mutex_unlock(&servers_lock);
    for (uint32_t i = 0; i < count; i++) {
      close(s->listeners[i].fd);
    }
    free_identities(s);
    GC_FREE(s);
    pts_throw_new(&pts_type_builtin_RangeError, "Too many HTTP servers");
  }
  uint32_t id = server_count++;
  servers[id] = s;
  uv_mutex_unlock(&servers_lock);
  start_listening(&s->listeners[0]);
  for (uint32_t i = 1; i < count; i++) {
    pts_post_call(s->listeners[i].loop, start_listening, &s->listeners[i]);
  }
  return id;
}

static server *server_of(double id) {
  uint32_t n = id >= 0 && id < MAX_SERVERS ? (uint32_t)id : MAX_SERVERS;
  uv_mutex_lock(&servers_lock);
  server *s = n < server_count ? servers[n] : NULL;
  uv_mutex_unlock(&servers_lock);
  if (s == NULL) {
    pts_throw_new(&pts_type_builtin_TypeError, "Not an HTTP server");
  }
  return s;
}

double pts_http_port(double id) { return server_of(id)->port; }

/*
 * Stops every loop's listener: this loop's now, the others' by a message, which comes
 * after the one that started it. A pool thread's call sends a message to each, its own
 * loop's too.
 */
void pts_http_stop(double id, bool close_active) {
  server *s = server_of(id);
  bool running = false;
  atomic_store(&s->close_active, close_active);
  if (!atomic_compare_exchange_strong(&s->stopped, &running, true)) {
    return;
  }
  for (uint32_t i = 0; i < s->listener_count; i++) {
    if (s->listeners[i].loop == pts_current_loop) {
      stop_listening(&s->listeners[i]);
    } else {
      pts_post_call(s->listeners[i].loop, stop_listening, &s->listeners[i]);
    }
  }
}
