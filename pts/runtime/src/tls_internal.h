/* TLS sessions over bytes their caller carries (tls.c, D-223): fetch's https requests. */
#ifndef PTS_TLS_INTERNAL_H
#define PTS_TLS_INTERNAL_H

#include "pts.h"

#include <stdbool.h>
#include <stddef.h>

typedef struct pts_tls pts_tls;

/* How a step of a session went. */
typedef enum pts_tls_status {
  PTS_TLS_DONE,   /* the step is complete */
  PTS_TLS_WANT,   /* it needs more bytes from the peer */
  PTS_TLS_CLOSED, /* the peer closed the session (close_notify) */
  PTS_TLS_FAILED, /* the session failed, and pts_tls_failure says why */
} pts_tls_status;

/*
 * A client session to host (a DNS name, or an IP address without brackets), which
 * verifies the server's certificate against the trust store and host; or NULL, with
 * *failure the reason, if it can't start.
 */
pts_tls *pts_tls_client(const char *host, pts_value *failure);

void pts_tls_free(pts_tls *tls);

/* Takes len bytes that came from the peer. */
void pts_tls_received(pts_tls *tls, const char *bytes, size_t len);

/* Whether there are bytes to send to the peer: if so, *bytes (malloc'd, now the caller's) and *len. */
bool pts_tls_take_output(pts_tls *tls, char **bytes, size_t *len);

/* Goes on with the handshake: DONE once it is complete and the server's certificate verified. */
pts_tls_status pts_tls_handshake(pts_tls *tls);

/* Encrypts len bytes for the peer, which pts_tls_take_output then gives. */
pts_tls_status pts_tls_write(pts_tls *tls, const char *bytes, size_t len);

/* Decrypts what has come, up to cap bytes into out: DONE with *got of them (at least 1), or what stops it. */
pts_tls_status pts_tls_read(pts_tls *tls, char *out, size_t cap, size_t *got);

/* Why the session failed, as fetch's TypeError's cause: Node's errors for certificates, mbedTLS's description otherwise. */
pts_value pts_tls_failure(const pts_tls *tls);

#endif
