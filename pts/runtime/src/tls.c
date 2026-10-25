/*
 * TLS for fetch's https requests (D-223), with mbedTLS. A session keeps the bytes that
 * came from the peer until mbedTLS reads them and the bytes mbedTLS writes until
 * fetch.c sends them, so mbedTLS never touches a socket and each step runs on the
 * request's loop, as the rest of the request does.
 *
 * The server's certificate is always verified: its chain against the trust store by
 * mbedTLS, and its names against the URL's host by on_verify, as Node's
 * tls.checkServerIdentity checks them (whose rules differ from mbedTLS's own). SNI names
 * the host, unless it is an IP address (as RFC 6066 and Node have it). The trust store is the system's CA bundle or SSL_CERT_FILE's, the CAs in
 * SSL_CERT_DIR's directories (load_roots), and NODE_EXTRA_CA_CERTS's. It and the client
 * configuration are made once, at the first request; after that every loop's sessions
 * only read them.
 *
 * Sessions are malloc'd and hold no collected pointers.
 */
#include "tls_internal.h"

#include <arpa/inet.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <uv.h>

#include <mbedtls/error.h>
#include <mbedtls/oid.h>
#include <mbedtls/psa_util.h>
#include <mbedtls/ssl.h>
#include <mbedtls/x509_crt.h>
#include <psa/crypto.h>

struct pts_tls {
  mbedtls_ssl_context ssl;
  char *in; /* bytes from the peer, of which mbedTLS has read the first in_read */
  size_t in_len, in_read, in_cap;
  char *out; /* bytes for the peer */
  size_t out_len, out_cap;
  uint8_t address[16]; /* the host's IP address, if it is one */
  size_t address_len;  /* 4 or 16 if it is, else 0 */
  int error;           /* mbedTLS's error that ended the session */
  /* What verifying the server's certificate saw, for the failure's message. */
  int chain_length;     /* of the chain mbedTLS built from it: the server's certificates, and a root if one is trusted */
  bool top_self_issued; /* the chain's last certificate issued itself */
  bool issuer_refused;  /* the server sent its certificate's issuer, which mbedTLS didn't take as one (not a CA) */
  bool names_checked;   /* on_verify checked the certificate's names */
  char *mismatch;       /* Node's reason the certificate doesn't name the host, if it doesn't */
  size_t host_len;
  char host[]; /* the URL's host, without a final dot */
};

static void *checked(void *memory) {
  if (memory == NULL) {
    pts_panic("RangeError", "Out of memory in TLS");
  }
  return memory;
}

/* ---------------------------------------------------------------- the trust store and configuration */

/* Where Linux distributions keep their CA bundle, as Go's crypto/x509 looks for it. */
static const char *const CA_BUNDLES[] = {
    "/etc/ssl/certs/ca-certificates.crt",                /* Debian, Ubuntu, Arch */
    "/etc/pki/tls/certs/ca-bundle.crt",                  /* Fedora, RHEL 6 */
    "/etc/ssl/ca-bundle.pem",                            /* openSUSE */
    "/etc/pki/tls/cacert.pem",                           /* OpenELEC */
    "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", /* CentOS, RHEL 7 */
    "/etc/ssl/cert.pem",                                 /* Alpine, macOS */
};

/* Where they keep their CAs one per file. */
static const char *const CA_DIRECTORIES[] = {"/etc/ssl/certs", "/etc/pki/tls/certs"};

/* The protocol fetch speaks inside TLS, offered with ALPN as undici does. */
static const char *ALPN[] = {"http/1.1", NULL};

static struct {
  int error;            /* mbedTLS's error if the configuration couldn't be made */
  psa_status_t psa;     /* PSA's if its subsystem didn't start */
  mbedtls_x509_crt roots;
  mbedtls_ssl_config config;
} client_setup;
static uv_once_t client_once = UV_ONCE_INIT;

/* Adds the certificates in each of the colon-separated directories to roots. */
static void load_directories(mbedtls_x509_crt *roots, const char *directories) {
  char *list = checked(strdup(directories)), *rest = list, *directory;
  while ((directory = strsep(&rest, ":")) != NULL) {
    if (*directory != '\0') {
      mbedtls_x509_crt_parse_path(roots, directory);
    }
  }
  free(list);
}

/* Adds NODE_EXTRA_CA_CERTS's certificates to roots, warning as Node does if it can't. */
static void load_extra(mbedtls_x509_crt *roots, const char *file) {
  int error = mbedtls_x509_crt_parse_file(roots, file);
  if (error < 0) {
    char reason[160];
    mbedtls_strerror(error, reason, sizeof reason);
    fprintf(stderr, "Warning: Ignoring extra certs from `%s`, load failed: %s\n", file, reason);
  }
}

/*
 * Fills roots with the trust store's certificates: SSL_CERT_FILE's bundle or the system's,
 * the certificates in SSL_CERT_DIR's directories or in the usual ones, and
 * NODE_EXTRA_CA_CERTS's. The usual directories hold the usual bundle's certificates, so
 * they are read only without one of those. Certificates mbedTLS can't parse are left out.
 */
static void load_roots(mbedtls_x509_crt *roots) {
  const char *file = getenv("SSL_CERT_FILE"), *directories = getenv("SSL_CERT_DIR"), *extra = getenv("NODE_EXTRA_CA_CERTS");
  bool system_bundle = false;
  if (file != NULL && *file != '\0') {
    mbedtls_x509_crt_parse_file(roots, file);
  }
  for (size_t i = 0; (file == NULL || *file == '\0') && !system_bundle && i < sizeof CA_BUNDLES / sizeof CA_BUNDLES[0]; i++) {
    system_bundle = access(CA_BUNDLES[i], R_OK) == 0 && mbedtls_x509_crt_parse_file(roots, CA_BUNDLES[i]) >= 0;
  }
  if (directories != NULL && *directories != '\0') {
    load_directories(roots, directories);
  }
  for (size_t i = 0; directories == NULL && !system_bundle && i < sizeof CA_DIRECTORIES / sizeof CA_DIRECTORIES[0]; i++) {
    mbedtls_x509_crt_parse_path(roots, CA_DIRECTORIES[i]);
  }
  if (extra != NULL && *extra != '\0') {
    load_extra(roots, extra);
  }
}

/* Makes the clients' configuration: certificates verified against the trust store, TLS 1.2 or 1.3, PSA's random numbers. */
static void make_client_setup(void) {
  mbedtls_x509_crt_init(&client_setup.roots);
  mbedtls_ssl_config_init(&client_setup.config);
  client_setup.psa = psa_crypto_init();
  if (client_setup.psa != PSA_SUCCESS) {
    return;
  }
  load_roots(&client_setup.roots);
  mbedtls_ssl_config *config = &client_setup.config;
  int error = mbedtls_ssl_config_defaults(config, MBEDTLS_SSL_IS_CLIENT, MBEDTLS_SSL_TRANSPORT_STREAM, MBEDTLS_SSL_PRESET_DEFAULT);
  if (error == 0) {
    error = mbedtls_ssl_conf_alpn_protocols(config, ALPN);
  }
  client_setup.error = error;
  mbedtls_ssl_conf_authmode(config, MBEDTLS_SSL_VERIFY_REQUIRED);
  mbedtls_ssl_conf_ca_chain(config, &client_setup.roots, NULL);
  mbedtls_ssl_conf_rng(config, mbedtls_psa_get_random, MBEDTLS_PSA_RANDOM_STATE);
}

/* ---------------------------------------------------------------- failures */

static pts_value error_of(const char *message) {
  return pts_value_from_object(pts_error_new(&pts_type_builtin_Error, pts_string_from_cstr(message)));
}

/* An Error with mbedTLS's description of error and its number: "SSL - An invalid SSL record was received (mbedTLS -0x7200)". */
static pts_value described(int error) {
  char text[256];
  mbedtls_strerror(error, text, sizeof text);
  size_t len = strlen(text);
  snprintf(text + len, sizeof text - len, " (mbedTLS -0x%04X)", (unsigned)-error);
  return error_of(text);
}

/*
 * Node's error for a certificate that failed verification with flags. For its chain,
 * OpenSSL's code and text for the error it finds last, as Node reports it: a validity
 * period after a purpose after trust. For its names, which Node checks only once the
 * chain verifies, tls.checkServerIdentity's.
 */
static pts_value verify_failure(const pts_tls *tls, uint32_t flags) {
  const char *code = "CERT_REJECTED", *message = "certificate rejected";
  if (flags & MBEDTLS_X509_BADCERT_EXPIRED) {
    code = "CERT_HAS_EXPIRED", message = "certificate has expired";
  } else if (flags & MBEDTLS_X509_BADCERT_FUTURE) {
    code = "CERT_NOT_YET_VALID", message = "certificate is not yet valid";
  } else if ((flags & (MBEDTLS_X509_BADCERT_KEY_USAGE | MBEDTLS_X509_BADCERT_EXT_KEY_USAGE | MBEDTLS_X509_BADCERT_NS_CERT_TYPE)) ||
             ((flags & MBEDTLS_X509_BADCERT_NOT_TRUSTED) && tls->issuer_refused)) {
    code = "INVALID_PURPOSE", message = "unsuitable certificate purpose";
  } else if (flags & MBEDTLS_X509_BADCERT_NOT_TRUSTED) {
    if (tls->top_self_issued) {
      code = tls->chain_length == 1 ? "DEPTH_ZERO_SELF_SIGNED_CERT" : "SELF_SIGNED_CERT_IN_CHAIN";
      message = tls->chain_length == 1 ? "self-signed certificate" : "self-signed certificate in certificate chain";
    } else {
      code = tls->chain_length == 1 ? "UNABLE_TO_VERIFY_LEAF_SIGNATURE" : "UNABLE_TO_GET_ISSUER_CERT_LOCALLY";
      message = tls->chain_length == 1 ? "unable to verify the first certificate" : "unable to get local issuer certificate";
    }
  } else if (flags & MBEDTLS_X509_BADCERT_BAD_MD) {
    code = "CA_MD_TOO_WEAK", message = "ca md too weak";
  } else if (flags & MBEDTLS_X509_BADCERT_BAD_KEY) {
    code = "EE_KEY_TOO_SMALL", message = "ee key too small";
  } else if ((flags & MBEDTLS_X509_BADCERT_CN_MISMATCH) && tls->mismatch != NULL) {
    static const char prefix[] = "Hostname/IP does not match certificate's altnames: ";
    pts_string *text = pts_string_concat(pts_string_from_cstr(prefix), pts_string_from_utf8_lossy(tls->mismatch, strlen(tls->mismatch)));
    return pts_coded_error_new(text, "ERR_TLS_CERT_ALTNAME_INVALID");
  }
  return pts_coded_error_new(pts_string_from_cstr(message), code);
}

pts_value pts_tls_failure(const pts_tls *tls) {
  uint32_t flags = mbedtls_ssl_get_verify_result(&tls->ssl);
  if ((tls->error == MBEDTLS_ERR_X509_CERT_VERIFY_FAILED || tls->error == MBEDTLS_ERR_SSL_BAD_CERTIFICATE) && flags != 0 && flags != UINT32_MAX) {
    return verify_failure(tls, flags);
  }
  return described(tls->error);
}

/* ---------------------------------------------------------------- the server's names */

static unsigned char lower(unsigned char c) { return c >= 'A' && c <= 'Z' ? (unsigned char)(c + 32) : c; }

static bool equal_ignoring_case(const void *a, const void *b, size_t len) {
  const unsigned char *x = a, *y = b;
  for (size_t i = 0; i < len; i++) {
    if (lower(x[i]) != lower(y[i])) {
      return false;
    }
  }
  return true;
}

/* How many labels a name of len bytes has: one more than its dots. */
static size_t labels(const void *name, size_t len) {
  size_t count = 1;
  for (const char *p = name, *end = p + len; (p = memchr(p, '.', (size_t)(end - p))) != NULL; p++) {
    count++;
  }
  return count;
}

/* Whether a label of len bytes holds an IDNA A-label's prefix, xn--, which takes no wildcard. */
static bool holds_idna_prefix(const unsigned char *label, size_t len) {
  for (size_t i = 0; i + 4 <= len; i++) {
    if (equal_ignoring_case(label + i, "xn--", 4)) {
      return true;
    }
  }
  return false;
}

/*
 * Whether a certificate's DNS name (a subject alternative name or a common name, len
 * bytes) names host, as Node's check() decides: as many labels as the host, none empty,
 * printable ASCII only, all but the first equal to the host's ignoring case; and the
 * first equal too, or matched by one wildcard (a*b) in a name of three labels or more,
 * but not in an IDNA label.
 */
static bool pattern_names(const unsigned char *pattern, size_t len, const char *host, size_t host_len) {
  if (len > 0 && pattern[len - 1] == '.') {
    len--;
  }
  if (len == 0 || labels(pattern, len) != labels(host, host_len)) {
    return false;
  }
  for (size_t i = 0; i < len; i++) {
    if (pattern[i] < 0x21 || pattern[i] > 0x7F || (pattern[i] == '.' && (i == 0 || i + 1 == len || pattern[i + 1] == '.'))) {
      return false;
    }
  }
  const unsigned char *dot = memchr(pattern, '.', len);
  const char *host_dot = memchr(host, '.', host_len);
  size_t first = dot != NULL ? (size_t)(dot - pattern) : len, host_first = host_dot != NULL ? (size_t)(host_dot - host) : host_len;
  if (len - first != host_len - host_first || !equal_ignoring_case(pattern + first, host + host_first, len - first)) {
    return false;
  }
  const unsigned char *star = memchr(pattern, '*', first);
  if (star == NULL || holds_idna_prefix(pattern, first)) {
    return first == host_first && equal_ignoring_case(pattern, host, first);
  }
  size_t prefix = (size_t)(star - pattern), suffix = first - prefix - 1;
  if (memchr(star + 1, '*', suffix) != NULL || labels(pattern, len) <= 2 || prefix + suffix > host_first) {
    return false;
  }
  return equal_ignoring_case(pattern, host, prefix) && equal_ignoring_case(star + 1, host + host_first - suffix, suffix);
}

/* The kind of a subject alternative name: MBEDTLS_X509_SAN_DNS_NAME, …_IP_ADDRESS, …. */
static int name_kind(const mbedtls_x509_sequence *name) { return name->buf.tag & MBEDTLS_ASN1_TAG_VALUE_MASK; }

/* Whether crt's subject alternative names hold the IP address of len bytes (4 or 16). */
static bool names_address(const mbedtls_x509_crt *crt, const uint8_t *address, size_t len) {
  for (const mbedtls_x509_sequence *name = &crt->subject_alt_names; name != NULL; name = name->next) {
    if (name_kind(name) == MBEDTLS_X509_SAN_IP_ADDRESS && name->buf.len == len && memcmp(name->buf.p, address, len) == 0) {
      return true;
    }
  }
  return false;
}

/* Writes an IP address of a certificate as OpenSSL and Node print its names (0:0:0:0:0:0:0:1), or canonically (::1). */
static void print_address(FILE *out, const unsigned char *bytes, size_t len, bool canonical) {
  char text[INET6_ADDRSTRLEN];
  if (len == 4 || (len == 16 && canonical)) {
    inet_ntop(len == 4 ? AF_INET : AF_INET6, bytes, text, sizeof text);
    fputs(text, out);
    return;
  }
  for (size_t i = 0; i + 1 < len; i += 2) {
    fprintf(out, "%s%X", i == 0 ? "" : ":", (unsigned)(bytes[i] << 8 | bytes[i + 1]));
  }
}

/*
 * Writes crt's subject alternative names as Node's cert.subjectaltname does ("DNS:a,
 * IP Address:127.0.0.1"); or, if addresses, only its IP addresses, canonical, as
 * tls.checkServerIdentity lists them.
 */
static void print_names(FILE *out, const mbedtls_x509_crt *crt, bool addresses) {
  bool first = true;
  for (const mbedtls_x509_sequence *name = &crt->subject_alt_names; name != NULL && name->buf.p != NULL; name = name->next) {
    int kind = name_kind(name);
    const char *label = kind == MBEDTLS_X509_SAN_DNS_NAME                      ? "DNS:"
                        : kind == MBEDTLS_X509_SAN_IP_ADDRESS                  ? "IP Address:"
                        : kind == MBEDTLS_X509_SAN_UNIFORM_RESOURCE_IDENTIFIER ? "URI:"
                        : kind == MBEDTLS_X509_SAN_RFC822_NAME                 ? "email:"
                                                                               : NULL;
    if (label == NULL || (addresses && kind != MBEDTLS_X509_SAN_IP_ADDRESS)) {
      continue;
    }
    fputs(first ? "" : ", ", out);
    first = false;
    if (!addresses) {
      fputs(label, out);
    }
    if (kind == MBEDTLS_X509_SAN_IP_ADDRESS) {
      print_address(out, name->buf.p, name->buf.len, addresses);
    } else {
      fwrite(name->buf.p, 1, name->buf.len, out);
    }
  }
}

/* Whether host names a DNS name of crt's subject alternative names: *some if it has any. */
static bool dns_name_names(const mbedtls_x509_crt *crt, const char *host, size_t host_len, bool *some) {
  for (const mbedtls_x509_sequence *name = &crt->subject_alt_names; name != NULL && name->buf.p != NULL; name = name->next) {
    if (name_kind(name) == MBEDTLS_X509_SAN_DNS_NAME) {
      *some = true;
      if (pattern_names(name->buf.p, name->buf.len, host, host_len)) {
        return true;
      }
    }
  }
  return false;
}

/* Whether a common name of crt names host: *some if it has any, printed to out. */
static bool common_name_names(const mbedtls_x509_crt *crt, const char *host, size_t host_len, bool *some, FILE *out) {
  for (const mbedtls_x509_name *name = &crt->subject; name != NULL; name = name->next) {
    if (MBEDTLS_OID_CMP(MBEDTLS_OID_AT_CN, &name->oid) == 0 && name->val.len > 0) {
      fputs(*some ? "," : "", out);
      fwrite(name->val.p, 1, name->val.len, out);
      *some = true;
      if (pattern_names(name->val.p, name->val.len, host, host_len)) {
        return true;
      }
    }
  }
  return false;
}

/*
 * Whether crt names the server's host, as Node's tls.checkServerIdentity decides: an IP
 * address by the certificate's IP addresses alone, a DNS name by its DNS names or, if it
 * has none, its common names. If not, *reason (malloc'd) is Node's reason.
 */
static bool names_host(const pts_tls *tls, const mbedtls_x509_crt *crt, char **reason) {
  size_t len = 0, common_len = 0;
  char *common = NULL;
  bool some_names = false, some_common = false, named = false;
  FILE *out = checked(open_memstream(reason, &len)), *common_out = checked(open_memstream(&common, &common_len));
  if (tls->address_len > 0) {
    named = names_address(crt, tls->address, tls->address_len);
    fprintf(out, "IP: %s is not in the cert's list: ", tls->host);
    print_names(out, crt, true);
  } else if ((named = dns_name_names(crt, tls->host, tls->host_len, &some_names)) || some_names) {
    fprintf(out, "Host: %s. is not in the cert's altnames: ", tls->host);
    print_names(out, crt, false);
  } else if ((named = common_name_names(crt, tls->host, tls->host_len, &some_common, common_out)) || some_common) {
    fflush(common_out);
    fprintf(out, "Host: %s. is not cert's CN: %s", tls->host, common);
  } else {
    fputs("Cert does not contain a DNS name", out);
  }
  fclose(common_out);
  free(common);
  fclose(out);
  if (named) {
    free(*reason);
    *reason = NULL;
  }
  return named;
}

/* Whether the server sent the certificate that issued crt, by its name. */
static bool issuer_presented(const mbedtls_x509_crt *crt) {
  for (const mbedtls_x509_crt *other = crt->next; other != NULL; other = other->next) {
    if (other->subject_raw.len == crt->issuer_raw.len && memcmp(other->subject_raw.p, crt->issuer_raw.p, crt->issuer_raw.len) == 0) {
      return true;
    }
  }
  return false;
}

/*
 * mbedTLS calls this for each certificate of the chain it built from the server's, from
 * the top down to the server's own (depth 0), with what it found wrong in flags. At the
 * server's own, the names are checked by Node's rules instead of mbedTLS's, which take no
 * common name when there are IP addresses and take a wildcard of a top-level domain.
 */
static int on_verify(void *data, mbedtls_x509_crt *crt, int depth, uint32_t *flags) {
  pts_tls *tls = data;
  if (tls->chain_length == 0) {
    tls->chain_length = depth + 1;
    tls->top_self_issued = crt->issuer_raw.len == crt->subject_raw.len && memcmp(crt->issuer_raw.p, crt->subject_raw.p, crt->subject_raw.len) == 0;
  }
  if (depth == 0) {
    tls->issuer_refused = tls->chain_length == 1 && !tls->top_self_issued && issuer_presented(crt);
    free(tls->mismatch);
    tls->mismatch = NULL;
    *flags &= ~(uint32_t)MBEDTLS_X509_BADCERT_CN_MISMATCH;
    if (!names_host(tls, crt, &tls->mismatch)) {
      *flags |= MBEDTLS_X509_BADCERT_CN_MISMATCH;
    }
    tls->names_checked = true;
  }
  return 0;
}

/* ---------------------------------------------------------------- sessions */

/* mbedTLS's output, kept for pts_tls_take_output. */
static int send_out(void *data, const unsigned char *bytes, size_t len) {
  pts_tls *tls = data;
  if (tls->out_cap - tls->out_len < len) {
    tls->out_cap = (tls->out_len + len) * 2;
    tls->out = checked(realloc(tls->out, tls->out_cap));
  }
  memcpy(tls->out + tls->out_len, bytes, len);
  tls->out_len += len;
  return (int)len;
}

/* mbedTLS's input: what has come from the peer that it hasn't read. */
static int receive_in(void *data, unsigned char *bytes, size_t len) {
  pts_tls *tls = data;
  size_t available = tls->in_len - tls->in_read;
  if (available == 0) {
    return MBEDTLS_ERR_SSL_WANT_READ;
  }
  size_t n = len < available ? len : available;
  memcpy(bytes, tls->in + tls->in_read, n);
  tls->in_read += n;
  return (int)n;
}

/* The IP address host spells, in *address: its length (4 or 16), or 0 if it isn't one. */
static size_t parse_address(const char *host, uint8_t *address) {
  return inet_pton(AF_INET, host, address) == 1 ? 4 : inet_pton(AF_INET6, host, address) == 1 ? 16 : 0;
}

pts_tls *pts_tls_client(const char *host, pts_value *failure) {
  uv_once(&client_once, make_client_setup);
  if (client_setup.psa != PSA_SUCCESS) {
    char message[64];
    snprintf(message, sizeof message, "TLS can't start: PSA crypto failed (%d)", (int)client_setup.psa);
    *failure = error_of(message);
    return NULL;
  }
  if (client_setup.error != 0) {
    *failure = described(client_setup.error);
    return NULL;
  }
  size_t host_len = strlen(host);
  host_len -= host_len > 0 && host[host_len - 1] == '.';
  pts_tls *tls = checked(calloc(1, sizeof *tls + host_len + 1));
  memcpy(tls->host, host, host_len);
  tls->host_len = host_len;
  tls->address_len = parse_address(tls->host, tls->address);
  mbedtls_ssl_init(&tls->ssl);
  int error = mbedtls_ssl_setup(&tls->ssl, &client_setup.config);
  if (error == 0) {
    /* NULL for an address: no SNI. */
    error = mbedtls_ssl_set_hostname(&tls->ssl, tls->address_len > 0 ? NULL : tls->host);
  }
  if (error != 0) {
    *failure = described(error);
    pts_tls_free(tls);
    return NULL;
  }
  mbedtls_ssl_set_bio(&tls->ssl, tls, send_out, receive_in, NULL);
  mbedtls_ssl_set_verify(&tls->ssl, on_verify, tls);
  return tls;
}

void pts_tls_free(pts_tls *tls) {
  mbedtls_ssl_free(&tls->ssl);
  free(tls->in);
  free(tls->out);
  free(tls->mismatch);
  free(tls);
}

void pts_tls_received(pts_tls *tls, const char *bytes, size_t len) {
  if (tls->in_read > 0) {
    memmove(tls->in, tls->in + tls->in_read, tls->in_len - tls->in_read);
    tls->in_len -= tls->in_read;
    tls->in_read = 0;
  }
  if (tls->in_cap - tls->in_len < len) {
    tls->in_cap = (tls->in_len + len) * 2;
    tls->in = checked(realloc(tls->in, tls->in_cap));
  }
  memcpy(tls->in + tls->in_len, bytes, len);
  tls->in_len += len;
}

bool pts_tls_take_output(pts_tls *tls, char **bytes, size_t *len) {
  if (tls->out_len == 0) {
    return false;
  }
  *bytes = tls->out;
  *len = tls->out_len;
  tls->out = NULL;
  tls->out_len = tls->out_cap = 0;
  return true;
}

/* What mbedTLS's result means for the step; a failure is kept for pts_tls_failure. */
static pts_tls_status status_of(pts_tls *tls, int result) {
  if (result >= 0) {
    return PTS_TLS_DONE;
  }
  if (result == MBEDTLS_ERR_SSL_WANT_READ) {
    return PTS_TLS_WANT;
  }
  if (result == MBEDTLS_ERR_SSL_PEER_CLOSE_NOTIFY) {
    return PTS_TLS_CLOSED;
  }
  tls->error = result;
  return PTS_TLS_FAILED;
}

pts_tls_status pts_tls_handshake(pts_tls *tls) {
  pts_tls_status status = status_of(tls, mbedtls_ssl_handshake(&tls->ssl));
  if (status == PTS_TLS_DONE && !tls->names_checked) {
    tls->error = MBEDTLS_ERR_X509_FATAL_ERROR; /* never: every handshake verifies the server's certificate, through on_verify */
    return PTS_TLS_FAILED;
  }
  return status;
}

pts_tls_status pts_tls_write(pts_tls *tls, const char *bytes, size_t len) {
  while (len > 0) {
    int written = mbedtls_ssl_write(&tls->ssl, (const unsigned char *)bytes, len);
    if (written < 0) {
      tls->error = written; /* not WANT_READ: send_out takes everything, and the handshake is over */
      return PTS_TLS_FAILED;
    }
    bytes += written;
    len -= (size_t)written;
  }
  return PTS_TLS_DONE;
}

pts_tls_status pts_tls_read(pts_tls *tls, char *out, size_t cap, size_t *got) {
  int result = mbedtls_ssl_read(&tls->ssl, (unsigned char *)out, cap);
  if (result > 0) {
    *got = (size_t)result;
    return PTS_TLS_DONE;
  }
  return result == 0 ? PTS_TLS_CLOSED : status_of(tls, result);
}
