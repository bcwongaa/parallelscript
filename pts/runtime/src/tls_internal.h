/* TLS sessions over bytes their caller carries (tls.c, D-223): fetch's https requests and pts:http's https servers. */
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

/*
 * A server's certificate chain (its own first, then the intermediates it sends) and private
 * key, from PEM text (NUL-terminated, len counting the NUL) or, for the key, DER, with the
 * configuration its sessions share; or NULL, with *failure the Error that says why.
 * passphrase is NULL for a key that isn't encrypted. One loop's sessions use it: signing
 * isn't shared across loops.
 */
typedef struct pts_tls_identity pts_tls_identity;
pts_tls_identity *pts_tls_identity_new(const char *cert, size_t cert_len, const char *key, size_t key_len, const pts_string *passphrase, pts_value *failure);

/* identity's private key in DER, decrypted (malloc'd, *len bytes, for pts_tls_forget); NULL if mbedTLS can't write it. */
char *pts_tls_identity_key(const pts_tls_identity *identity, size_t *len);

void pts_tls_identity_free(pts_tls_identity *identity);

/* Wipes len bytes of secrets (a private key's text) and frees them. */
void pts_tls_forget(char *bytes, size_t len);

/* A server session that presents identity's certificate; or NULL, with *failure the reason, if it can't start. */
pts_tls *pts_tls_server(pts_tls_identity *identity, pts_value *failure);

void pts_tls_free(pts_tls *tls);

/* Takes len bytes that came from the peer. */
void pts_tls_received(pts_tls *tls, const char *bytes, size_t len);

/* Whether there are bytes to send to the peer: if so, *bytes (malloc'd, now the caller's) and *len. */
bool pts_tls_take_output(pts_tls *tls, char **bytes, size_t *len);

/* Goes on with the handshake: DONE once it is complete and, for a client, the server's certificate verified. */
pts_tls_status pts_tls_handshake(pts_tls *tls);

/* Encrypts len bytes for the peer, which pts_tls_take_output then gives; FAILED once the session has failed. */
pts_tls_status pts_tls_write(pts_tls *tls, const char *bytes, size_t len);

/* Ends the session with close_notify, which pts_tls_take_output then gives; once, whoever asks again, and not after a failure. */
pts_tls_status pts_tls_close(pts_tls *tls);

/* Decrypts what has come, up to cap bytes into out: DONE with *got of them (at least 1), or what stops it. */
pts_tls_status pts_tls_read(pts_tls *tls, char *out, size_t cap, size_t *got);

/* Why the session failed (as fetch's TypeError's cause): Node's errors for a server's certificate, mbedTLS's description otherwise. */
pts_value pts_tls_failure(const pts_tls *tls);

#endif
