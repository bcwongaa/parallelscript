/*
 * fetch's HTTP/1.1 client (D-O10, D-160). Each request is one connection on the calling
 * loop: it resolves the host (uv_getaddrinfo), tries the addresses in order until one
 * connects, writes the request with `connection: close`, and reads the response: its
 * head (skipping 1xx ones), then a body framed by Content-Length, by chunks, or by the
 * connection's end. The promise pts_http_fetch returns settles with a string[] of the
 * status, the status text, the body (decoded as UTF-8, as text() does) and the header
 * fields, names lowercased and values trimmed, in turn; or it rejects with the error that
 * fetch gives as its TypeError's cause (Node's system errors for the system calls).
 *
 * The client and libuv's handles are malloc'd and hold no collected pointers; the
 * promise is in uncollectable memory until it settles.
 */
#include "pts.h"

#include "http_internal.h"
#include "scheduler_internal.h"

#include <arpa/inet.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <uv.h>

enum {
  MAX_HEAD = 64 * 1024, /* a response's status line and headers */
  READ_SIZE = 64 * 1024,
};

/* What the collector must see while a request is in flight. */
typedef struct roots {
  pts_value promise;
} roots;

typedef struct client {
  uv_tcp_t tcp; /* open while tcp_open */
  uv_getaddrinfo_t resolver;
  uv_connect_t connect;
  uv_write_t write;
  bool tcp_open;
  bool done;      /* settled: callbacks of the connection's closing do nothing more */
  bool head_only; /* a HEAD request, whose response has no body */
  char *request;  /* the request's bytes */
  size_t request_len;
  char *host;
  int port;
  struct addrinfo *addresses, *next; /* what the host resolved to, and the next to try */
  char address[INET6_ADDRSTRLEN + 8]; /* the last address tried, "127.0.0.1:80", for messages */
  int error;                          /* why it didn't connect */
  char *in;                           /* the bytes read */
  size_t in_len, in_cap;
  roots *roots;
} client;

static void *checked(void *memory) {
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory fetching");
  }
  return memory;
}

static void free_client(client *c) {
  if (c->addresses != NULL) {
    uv_freeaddrinfo(c->addresses);
  }
  free(c->request);
  free(c->host);
  free(c->in);
  free(c);
}

static void on_closed_free(uv_handle_t *handle) { free_client(handle->data); }

/* Ends the request: closes the connection (the client is freed once it has closed) and settles the promise. */
static void finish(client *c, bool fulfilled, pts_value result) {
  roots *r = c->roots;
  c->done = true;
  if (c->tcp_open) {
    uv_close((uv_handle_t *)&c->tcp, on_closed_free);
  } else {
    free_client(c);
  }
  pts_value promise = r->promise;
  GC_FREE(r);
  pts_promise_settle(promise, fulfilled, result);
  pts_run_microtasks();
}

/* Fails with an Error of the message (a protocol error, as undici's). */
static void fail_with(client *c, const char *message) {
  finish(c, false, pts_value_from_object(pts_error_new(&pts_type_builtin_Error, pts_string_from_cstr(message))));
}

/* Fails with Node's system error for a system call that failed with error: "syscall CODE detail". */
static void fail_system(client *c, int error, const char *syscall, const char *code, const char *detail) {
  if (code == NULL) {
    code = uv_err_name(error);
  }
  size_t size = strlen(syscall) + strlen(code) + strlen(detail) + 3;
  char *message = checked(malloc(size));
  snprintf(message, size, "%s %s%s%s", syscall, code, *detail != '\0' ? " " : "", detail);
  pts_value failure = pts_system_error_new(error, pts_string_from_cstr(message), syscall, NULL, NULL);
  free(message);
  ((pts_system_error *)pts_value_as_object(failure))->code = pts_value_from_string(pts_string_from_cstr(code));
  finish(c, false, failure);
}

/* ---------------------------------------------------------------- the response */

/* What a response's head says. */
typedef struct head {
  size_t len; /* of the head, its blank line included */
  int status;
  const char *reason;
  size_t reason_len;
  int64_t content_length; /* -1 if not given */
  bool chunked;
} head;

/* The end of the line starting at p (its \n), or NULL if there is none before end. */
static const char *line_end(const char *p, const char *end) { return memchr(p, '\n', (size_t)(end - p)); }

/* The length of a line without its \r\n or \n. */
static size_t line_len(const char *p, const char *newline) { return (size_t)(newline - p) - (newline > p && newline[-1] == '\r'); }

/*
 * Parses the head at the start of bytes (len of them): 1 with *h filled in, 0 if the
 * bytes don't hold all of it yet, -1 if it is malformed. With fields, it also appends
 * each header's name (lowercased) and value (trimmed) to them.
 */
static int parse_head(const char *bytes, size_t len, head *h, pts_array *fields) {
  const char *end = bytes + len, *newline = line_end(bytes, end);
  if (newline == NULL) {
    return 0;
  }
  size_t status_len = line_len(bytes, newline);
  if (status_len < 12 || strncmp(bytes, "HTTP/1.", 7) != 0 || bytes[8] != ' ' || (status_len > 12 && bytes[12] != ' ')) {
    return -1;
  }
  h->status = 0;
  for (int i = 9; i < 12; i++) {
    if (bytes[i] < '0' || bytes[i] > '9') {
      return -1;
    }
    h->status = h->status * 10 + (bytes[i] - '0');
  }
  h->reason = status_len > 12 ? bytes + 13 : bytes + 12;
  h->reason_len = status_len > 12 ? status_len - 13 : 0;
  h->content_length = -1;
  h->chunked = false;
  for (const char *p = newline + 1;;) {
    newline = line_end(p, end);
    if (newline == NULL) {
      return 0;
    }
    size_t n = line_len(p, newline);
    if (n == 0) {
      h->len = (size_t)(newline + 1 - bytes);
      return 1;
    }
    const char *colon = memchr(p, ':', n);
    if (colon == NULL || colon == p) {
      return -1;
    }
    size_t name_len = (size_t)(colon - p);
    const char *value = colon + 1, *value_end = p + n;
    while (value < value_end && (*value == ' ' || *value == '\t')) {
      value++;
    }
    while (value_end > value && (value_end[-1] == ' ' || value_end[-1] == '\t')) {
      value_end--;
    }
    size_t value_len = (size_t)(value_end - value);
    if (pts_http_equals_ignoring_case(p, name_len, "content-length")) {
      int64_t length = 0;
      for (size_t i = 0; i < value_len; i++) {
        if (value[i] < '0' || value[i] > '9' || length > PTS_HTTP_MAX_BODY) {
          return -1;
        }
        length = length * 10 + (value[i] - '0');
      }
      if (value_len == 0 || (h->content_length >= 0 && h->content_length != length)) {
        return -1;
      }
      h->content_length = length;
    } else if (pts_http_equals_ignoring_case(p, name_len, "transfer-encoding")) {
      h->chunked = pts_http_lists(value, value_len, "chunked");
    }
    if (fields != NULL) {
      char *name = checked(malloc(name_len));
      for (size_t i = 0; i < name_len; i++) {
        name[i] = p[i] >= 'A' && p[i] <= 'Z' ? (char)(p[i] + 32) : p[i];
      }
      pts_string *strings[] = {pts_string_from_utf8_lossy(name, name_len), pts_string_from_utf8_lossy(value, value_len)};
      free(name);
      pts_array_push(fields, strings, 2);
    }
    p = newline + 1;
  }
}

/* Fulfills the promise with the response whose head is h and whose body is body_len bytes at body. */
static void respond(client *c, const head *h, const char *body, size_t body_len) {
  char status[4];
  snprintf(status, sizeof status, "%d", h->status);
  pts_string *parts[] = {pts_string_from_cstr(status), pts_string_from_utf8_lossy(h->reason, h->reason_len),
                         pts_string_from_utf8_lossy(body, body_len)};
  pts_array *result = pts_array_of(&pts_array_types[PTS_FIELD_STRING], parts, 3);
  head again;
  parse_head(c->in, h->len, &again, result);
  finish(c, true, pts_value_from_object(result));
}

/* Goes on with the response so far; at_end when the connection has ended. */
static void advance(client *c, bool at_end) {
  head h;
  int parsed;
  for (;;) {
    parsed = parse_head(c->in, c->in_len, &h, NULL);
    if (parsed != 1 || h.status >= 200 || h.status == 101) {
      break;
    }
    memmove(c->in, c->in + h.len, c->in_len - h.len); /* a 1xx response before the response */
    c->in_len -= h.len;
  }
  if (parsed < 0 || (parsed == 1 && h.status < 100)) {
    fail_with(c, "Response does not match the HTTP/1.1 protocol");
    return;
  }
  if (parsed == 0) {
    if (c->in_len > MAX_HEAD) {
      fail_with(c, "Response headers are larger than 64 KB");
    } else if (at_end) {
      fail_with(c, "other side closed");
    }
    return;
  }
  const char *body = c->in + h.len, *end = c->in + c->in_len;
  size_t available = (size_t)(end - body);
  if (c->head_only || h.status == 204 || h.status == 304) {
    respond(c, &h, body, 0);
  } else if (h.chunked) {
    char *bytes = NULL;
    size_t len = 0, used = 0;
    int done = pts_http_dechunk(body, end, &bytes, &len, &used);
    if (done == 1) {
      respond(c, &h, bytes, len);
    } else if (done == -1) {
      fail_with(c, "Response does not match the HTTP/1.1 protocol");
    } else if (done == -2) {
      fail_with(c, "Response body is larger than 64 MB");
    } else if (at_end) {
      fail_with(c, "other side closed");
    }
    free(bytes);
  } else if (h.content_length >= 0) {
    if (available >= (size_t)h.content_length) {
      respond(c, &h, body, (size_t)h.content_length);
    } else if (at_end) {
      fail_with(c, "other side closed");
    }
  } else if (at_end) {
    respond(c, &h, body, available); /* the body is what comes before the connection ends */
  } else if (available > PTS_HTTP_MAX_BODY) {
    fail_with(c, "Response body is larger than 64 MB");
  }
}

static void on_alloc(uv_handle_t *handle, size_t suggested, uv_buf_t *buf) {
  (void)suggested;
  client *c = handle->data;
  if (c->in_cap - c->in_len < READ_SIZE) {
    c->in_cap = (c->in_len + READ_SIZE) * 2;
    c->in = checked(realloc(c->in, c->in_cap));
  }
  *buf = uv_buf_init(c->in + c->in_len, (unsigned)(c->in_cap - c->in_len));
}

static void on_read(uv_stream_t *stream, ssize_t nread, const uv_buf_t *buf) {
  (void)buf;
  client *c = stream->data;
  if (c->done) {
    return;
  }
  if (nread > 0) {
    c->in_len += (size_t)nread;
    advance(c, false);
  } else if (nread == UV_EOF) {
    advance(c, true);
  } else if (nread < 0) {
    fail_system(c, (int)nread, "read", NULL, "");
  }
}

static void on_written(uv_write_t *write, int status) {
  client *c = write->data;
  if (status < 0 && !c->done) {
    fail_system(c, status, "write", NULL, "");
  }
}

/* ---------------------------------------------------------------- connecting */

static void try_next(client *c);

static void on_closed_retry(uv_handle_t *handle) {
  client *c = handle->data;
  c->tcp_open = false;
  try_next(c);
}

static void on_connected(uv_connect_t *connect, int status) {
  client *c = connect->data;
  if (status < 0) {
    c->error = status;
    uv_close((uv_handle_t *)&c->tcp, on_closed_retry);
    return;
  }
  uv_buf_t out = uv_buf_init(c->request, (unsigned)c->request_len);
  c->write.data = c;
  int error = uv_write(&c->write, (uv_stream_t *)&c->tcp, &out, 1, on_written);
  if (error == 0) {
    error = uv_read_start((uv_stream_t *)&c->tcp, on_alloc, on_read);
  }
  if (error < 0) {
    fail_system(c, error, "write", NULL, "");
  }
}

/* Writes an address and port as Node's messages show them: 127.0.0.1:80, ::1:80. */
static void name_address(const struct sockaddr *address, char *out, size_t size) {
  char ip[INET6_ADDRSTRLEN] = "";
  int port;
  if (address->sa_family == AF_INET6) {
    uv_ip6_name((const struct sockaddr_in6 *)address, ip, sizeof ip);
    port = ntohs(((const struct sockaddr_in6 *)address)->sin6_port);
  } else {
    uv_ip4_name((const struct sockaddr_in *)address, ip, sizeof ip);
    port = ntohs(((const struct sockaddr_in *)address)->sin_port);
  }
  snprintf(out, size, "%s:%d", ip, port);
}

/* Connects to the next address the host resolved to, or fails with the last one's error. */
static void try_next(client *c) {
  while (c->next != NULL) {
    struct addrinfo *address = c->next;
    c->next = address->ai_next;
    name_address(address->ai_addr, c->address, sizeof c->address);
    int error = uv_tcp_init(pts_uv_loop(), &c->tcp);
    if (error < 0) {
      c->error = error;
      continue;
    }
    c->tcp.data = c;
    c->tcp_open = true;
    c->connect.data = c;
    error = uv_tcp_connect(&c->connect, &c->tcp, address->ai_addr, on_connected);
    if (error < 0) {
      c->error = error;
      uv_close((uv_handle_t *)&c->tcp, on_closed_retry);
    }
    return;
  }
  fail_system(c, c->error != 0 ? c->error : UV_EADDRNOTAVAIL, "connect", NULL, c->address);
}

static void on_resolved(uv_getaddrinfo_t *resolver, int status, struct addrinfo *addresses) {
  client *c = resolver->data;
  if (status < 0) {
    /* Node names a host that doesn't resolve ENOTFOUND. */
    const char *code = status == UV_EAI_NONAME || status == UV_EAI_NODATA ? "ENOTFOUND" : NULL;
    fail_system(c, status, "getaddrinfo", code, c->host);
    return;
  }
  c->addresses = c->next = addresses;
  try_next(c);
}

/* ---------------------------------------------------------------- the intrinsic */

static void add(char **bytes, size_t *len, size_t *cap, const char *more, size_t n) {
  if (*len + n > *cap) {
    *cap = (*len + n) * 2 + 256;
    *bytes = checked(realloc(*bytes, *cap));
  }
  memcpy(*bytes + *len, more, n);
  *len += n;
}

static void add_string(char **bytes, size_t *len, size_t *cap, const pts_string *s) { add(bytes, len, cap, s->bytes, s->byte_len); }

static void add_cstr(char **bytes, size_t *len, size_t *cap, const char *s) { add(bytes, len, cap, s, strlen(s)); }

pts_value pts_http_fetch(pts_string *method, pts_string *host, double port, pts_string *target, pts_array *headers, pts_string *body, bool has_body) {
  client *c = checked(calloc(1, sizeof *c));
  size_t cap = 0;
  add_string(&c->request, &c->request_len, &cap, method);
  add_cstr(&c->request, &c->request_len, &cap, " ");
  add_string(&c->request, &c->request_len, &cap, target);
  add_cstr(&c->request, &c->request_len, &cap, " HTTP/1.1\r\n");
  for (size_t i = 0; i + 1 < pts_array_length_of(headers); i += 2) {
    add_string(&c->request, &c->request_len, &cap, (pts_string *)pts_array_get_word(headers, (double)i));
    add_cstr(&c->request, &c->request_len, &cap, ": ");
    add_string(&c->request, &c->request_len, &cap, (pts_string *)pts_array_get_word(headers, (double)(i + 1)));
    add_cstr(&c->request, &c->request_len, &cap, "\r\n");
  }
  if (has_body) {
    char length[32];
    snprintf(length, sizeof length, "content-length: %u\r\n", body->byte_len);
    add_cstr(&c->request, &c->request_len, &cap, length);
  }
  add_cstr(&c->request, &c->request_len, &cap, "connection: close\r\n\r\n");
  if (has_body) {
    add_string(&c->request, &c->request_len, &cap, body);
  }
  c->head_only = pts_http_equals_ignoring_case(method->bytes, method->byte_len, "HEAD");
  c->host = checked(strndup(host->bytes, host->byte_len));
  c->port = (int)port;
  c->roots = checked(GC_MALLOC_UNCOLLECTABLE(sizeof *c->roots));
  pts_value promise = c->roots->promise = pts_promise_pending();
  char service[8];
  snprintf(service, sizeof service, "%d", c->port);
  struct addrinfo hints = {.ai_family = AF_UNSPEC, .ai_socktype = SOCK_STREAM};
  c->resolver.data = c;
  int error = uv_getaddrinfo(pts_uv_loop(), &c->resolver, on_resolved, c->host, service, &hints);
  if (error < 0) {
    fail_system(c, error, "getaddrinfo", NULL, c->host);
  }
  return promise;
}
