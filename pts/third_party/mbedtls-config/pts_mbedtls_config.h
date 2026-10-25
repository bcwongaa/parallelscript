/*
 * ParallelScript's additions to mbedTLS's default configuration (D-223), read after
 * include/mbedtls/mbedtls_config.h through MBEDTLS_USER_CONFIG_FILE, by the library's
 * build and by the runtime's tls.c alike.
 *
 * Every loop's thread runs handshakes, and they share the PSA subsystem's state (its
 * random generator and key slots): mbedTLS's own locks over pthreads guard it.
 */
#define MBEDTLS_THREADING_C
#define MBEDTLS_THREADING_PTHREAD

/* Not used: the debug log, DTLS and mbedTLS's own sockets and timers (libuv's carry the bytes). */
#undef MBEDTLS_DEBUG_C
#undef MBEDTLS_SSL_PROTO_DTLS
#undef MBEDTLS_SSL_DTLS_ANTI_REPLAY
#undef MBEDTLS_SSL_DTLS_HELLO_VERIFY
#undef MBEDTLS_SSL_DTLS_CLIENT_PORT_REUSE
#undef MBEDTLS_SSL_DTLS_CONNECTION_ID
#undef MBEDTLS_SSL_DTLS_SRTP
#undef MBEDTLS_SSL_COOKIE_C
#undef MBEDTLS_NET_C
#undef MBEDTLS_TIMING_C
