/* What pts:http's server (http.c) and fetch's client (fetch.c) share: header parsing and chunked bodies. */
#ifndef PTS_HTTP_INTERNAL_H
#define PTS_HTTP_INTERNAL_H

#include <stdbool.h>
#include <stddef.h>

enum { PTS_HTTP_MAX_BODY = 64 * 1024 * 1024 }; /* a body: a server answers 413 above it, a client fails */

/* Whether len bytes are word, ignoring ASCII case. */
bool pts_http_equals_ignoring_case(const char *bytes, size_t len, const char *word);

/* Whether a comma-separated header value lists word, ignoring case. */
bool pts_http_lists(const char *value, size_t len, const char *word);

/*
 * Decodes a chunked body that starts at body: the body's bytes into *out (malloc'd) and
 * the bytes it took from the buffer into *used. 0 if the buffer doesn't hold all of it
 * yet, -1 if it is malformed, -2 if it is larger than PTS_HTTP_MAX_BODY, 1 if done.
 */
int pts_http_dechunk(const char *body, const char *end, char **out, size_t *out_len, size_t *used);

#endif
