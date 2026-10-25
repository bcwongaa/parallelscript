/*
 * TLS for fetch's https requests (D-223) and pts:http's https servers (D-224), with
 * mbedTLS. A session keeps the bytes that came from the peer until mbedTLS reads them and
 * the bytes mbedTLS writes until fetch.c or http.c sends them, so mbedTLS never touches a
 * socket and each step runs on the loop of the request or connection, as the rest of it does.
 *
 * The server's certificate is always verified: its chain against the trust store by
 * mbedTLS, and its names against the URL's host by on_verify, as Node's
 * tls.checkServerIdentity checks them (whose rules differ from mbedTLS's own). SNI names
 * the host, unless it is an IP address (as RFC 6066 and Node have it). The trust store is the system's CA bundle or SSL_CERT_FILE's, the CAs in
 * SSL_CERT_DIR's directories (load_roots), and NODE_EXTRA_CA_CERTS's. It and the client
 * configuration are made once, at the first request; after that every loop's sessions
 * only read them.
 *
 * A server presents an identity: its certificate chain and key, with a configuration of
 * their own for each loop that accepts its connections, so that loops don't share a key's
 * state (an RSA key's lock would serialize their handshakes).
 *
 * Sessions and identities are malloc'd and hold no collected pointers.
 */
#include "tls_internal.h"

#include <arpa/inet.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <uv.h>

#include <mbedtls/asn1.h>
#include <mbedtls/error.h>
#include <mbedtls/oid.h>
#include <mbedtls/platform_util.h>
#include <mbedtls/psa_util.h>
#include <mbedtls/ssl.h>
#include <mbedtls/x509_crt.h>
#include <psa/crypto.h>

/*
 * Why mbedTLS didn't take a certificate (sent by the server, or in the trust store) that
 * names the chain's last certificate's issuer, where OpenSSL builds the chain by names and
 * then reports what is wrong with it.
 */
typedef enum refusal {
  REFUSED_NONE,
  REFUSED_NOT_CA,       /* not a CA */
  REFUSED_NO_CERT_SIGN, /* its key usage doesn't include signing certificates */
  REFUSED_PATH_LENGTH,  /* its path length constraint is exceeded */
  REFUSED_SIGNATURE,    /* a trusted root whose signature of the certificate doesn't verify */
} refusal;

struct pts_tls {
  mbedtls_ssl_context ssl;
  char *in; /* bytes from the peer, of which mbedTLS has read the first in_read */
  size_t in_len, in_read, in_cap;
  char *out; /* bytes for the peer */
  size_t out_len, out_cap;
  uint8_t address[16]; /* the host's IP address, if it is one */
  size_t address_len;  /* 4 or 16 if it is, else 0 */
  int error;           /* mbedTLS's error that ended the session */
  /* What verifying the server's certificate found (on_verify), for verify_failure. */
  uint32_t found[MBEDTLS_X509_MAX_VERIFY_CHAIN_SIZE]; /* each certificate's flags, by depth */
  bool self_issued[MBEDTLS_X509_MAX_VERIFY_CHAIN_SIZE]; /* each certificate issued itself */
  bool other_key[MBEDTLS_X509_MAX_VERIFY_CHAIN_SIZE];   /* each certificate's issuer key isn't its parent's in the chain */
  const mbedtls_x509_crt *above; /* the certificate on_verify saw last, above the next */
  int chain_length;     /* of the chain mbedTLS built: the server's certificates, and a root if one is trusted */
  bool top_self_issued; /* the chain's last certificate issued itself */
  refusal refused;      /* why mbedTLS didn't take an issuer of the chain's last certificate, if there is one */
  uint32_t refused_validity; /* that issuer's own validity flags (expired, not yet valid) */
  bool leaf_key_weak;   /* the server's own RSA key is shorter than mbedTLS takes */
  bool constrained;     /* a CA of the chain has name constraints, which aren't supported */
  bool names_checked;   /* on_verify checked the certificate's names */
  bool server;          /* the session is a server's, which verifies nothing */
  bool closed;          /* close_notify was written, which is sent once */
  char *mismatch;       /* Node's reason the certificate doesn't name the host, if it doesn't */
  size_t mismatch_len;
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
  mbedtls_x509_crt roots;
  mbedtls_ssl_config config;
} client_setup;
static uv_once_t client_once = UV_ONCE_INIT;

/* PSA's subsystem, which TLS 1.3 and the random generator need: started once, for clients and servers alike. */
static psa_status_t psa_status;
static uv_once_t psa_once = UV_ONCE_INIT;

static void start_psa(void) { psa_status = psa_crypto_init(); }

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
  if (psa_status != PSA_SUCCESS) {
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

/* An Error with prefix, mbedTLS's description of error and its number: "SSL - An invalid SSL record was received (mbedTLS -0x7200)". */
static pts_value described_after(const char *prefix, int error) {
  char text[320];
  size_t len = (size_t)snprintf(text, sizeof text, "%s", prefix);
  mbedtls_strerror(error, text + len, sizeof text - len);
  len = strlen(text);
  snprintf(text + len, sizeof text - len, " (mbedTLS -0x%04X)", (unsigned)-error);
  return error_of(text);
}

static pts_value described(int error) { return described_after("", error); }

/* Why PSA's subsystem didn't start, if it didn't: an Error, or PTS_VALUE_ABSENT. */
static pts_value psa_failure(void) {
  uv_once(&psa_once, start_psa);
  if (psa_status == PSA_SUCCESS) {
    return PTS_VALUE_ABSENT;
  }
  char message[64];
  snprintf(message, sizeof message, "TLS can't start: PSA crypto failed (%d)", (int)psa_status);
  return error_of(message);
}

/*
 * Node's error for a certificate that failed verification, flags being mbedTLS's for the
 * whole chain. For the chain, OpenSSL's code and text (UNSPECIFIED where Node has no name
 * for OpenSSL's error) for the error OpenSSL finds last, which Node reports: trust (the
 * chain it builds by names), then purposes and CA constraints, then keys and digests, then
 * each certificate's signature and validity period from the top down (so the server's own
 * last), then name constraints. For the names, which Node checks only once the chain
 * verifies, tls.checkServerIdentity's.
 */
static pts_value verify_failure(const pts_tls *tls, uint32_t flags) {
  const char *code = NULL, *message = NULL;
  /*
   * OpenSSL's chain ends below an issuer whose key isn't the one the certificate names
   * (by its authority key identifier), where mbedTLS takes it and finds the signature bad:
   * the certificate's issuer is then missing, and the flags of what is above don't count.
   */
  int top = tls->chain_length - 1;
  for (int depth = 0; depth < top; depth++) {
    if (tls->other_key[depth]) {
      top = depth;
    }
  }
  bool cut = top < tls->chain_length - 1, self_issued = cut ? tls->self_issued[top] : tls->top_self_issued;
  bool untrusted = cut || (top >= 0 && (tls->found[top] & MBEDTLS_X509_BADCERT_NOT_TRUSTED));
  refusal refused = cut ? REFUSED_NONE : tls->refused;
  uint32_t seen = 0, kept = 0;
  for (int depth = 0; depth < tls->chain_length; depth++) {
    seen |= tls->found[depth];
    kept |= depth <= top ? tls->found[depth] : 0;
  }
  flags = kept | (flags & ~seen); /* and what mbedTLS checked of the server's after the callback */
  bool lone_leaf = top == 0 && !self_issued && refused == REFUSED_NONE && untrusted;
  if (untrusted && refused == REFUSED_NONE) {
    if (self_issued) {
      code = top == 0 ? "DEPTH_ZERO_SELF_SIGNED_CERT" : "SELF_SIGNED_CERT_IN_CHAIN";
      message = top == 0 ? "self-signed certificate" : "self-signed certificate in certificate chain";
    } else {
      code = top == 0 ? "UNABLE_TO_VERIFY_LEAF_SIGNATURE" : "UNABLE_TO_GET_ISSUER_CERT_LOCALLY";
      message = top == 0 ? "unable to verify the first certificate" : "unable to get local issuer certificate";
    }
  }
  if (refused == REFUSED_NOT_CA || (flags & (MBEDTLS_X509_BADCERT_KEY_USAGE | MBEDTLS_X509_BADCERT_EXT_KEY_USAGE | MBEDTLS_X509_BADCERT_NS_CERT_TYPE))) {
    code = "INVALID_PURPOSE", message = "unsuitable certificate purpose";
  } else if (refused == REFUSED_NO_CERT_SIGN) {
    code = "UNSPECIFIED", message = "key usage does not include certificate signing";
  } else if (refused == REFUSED_PATH_LENGTH) {
    code = "PATH_LENGTH_EXCEEDED", message = "path length constraint exceeded";
  }
  if (flags & MBEDTLS_X509_BADCERT_BAD_KEY) {
    code = "UNSPECIFIED";
    message = (tls->found[0] & MBEDTLS_X509_BADCERT_BAD_KEY) && tls->leaf_key_weak ? "EE certificate key too weak" : "CA certificate key too weak";
  } else if (flags & MBEDTLS_X509_BADCERT_BAD_MD) {
    code = "UNSPECIFIED", message = "CA signature digest algorithm too weak";
  }
  if (refused != REFUSED_NONE && (tls->refused_validity & MBEDTLS_X509_BADCERT_EXPIRED)) {
    code = "CERT_HAS_EXPIRED", message = "certificate has expired"; /* the refused issuer, above the chain mbedTLS built */
  } else if (refused != REFUSED_NONE && (tls->refused_validity & MBEDTLS_X509_BADCERT_FUTURE)) {
    code = "CERT_NOT_YET_VALID", message = "certificate is not yet valid";
  }
  for (int depth = top; depth >= 0; depth--) {
    uint32_t found = tls->found[depth];
    if ((depth < top && (found & MBEDTLS_X509_BADCERT_NOT_TRUSTED)) || (depth == top && refused == REFUSED_SIGNATURE)) {
      code = "CERT_SIGNATURE_FAILURE", message = "certificate signature failure"; /* its issuer above it didn't sign it */
    }
    if (depth == 0 && lone_leaf) {
      code = "UNABLE_TO_VERIFY_LEAF_SIGNATURE", message = "unable to verify the first certificate";
    }
    if (found & MBEDTLS_X509_BADCERT_EXPIRED) {
      code = "CERT_HAS_EXPIRED", message = "certificate has expired";
    } else if (found & MBEDTLS_X509_BADCERT_FUTURE) {
      code = "CERT_NOT_YET_VALID", message = "certificate is not yet valid";
    }
  }
  if (tls->constrained) {
    code = "UNSPECIFIED", message = "name constraints are not supported: a CA that has them is not trusted";
  }
  if (code == NULL && (flags & MBEDTLS_X509_BADCERT_CN_MISMATCH) && tls->mismatch != NULL) {
    static const char prefix[] = "Hostname/IP does not match certificate's altnames: ";
    pts_string *text = pts_string_concat(pts_string_from_cstr(prefix), pts_string_from_utf8_lossy(tls->mismatch, tls->mismatch_len));
    return pts_coded_error_new(text, "ERR_TLS_CERT_ALTNAME_INVALID");
  }
  if (code == NULL) {
    code = "CERT_REJECTED", message = "certificate rejected";
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
 * Writes a name of len bytes as Node writes a subject alternative name that isn't UTF-8
 * (PrintAltName): as it is if it is printable ASCII without `"`, `\`, `,` or `'`, else in
 * double quotes with `\` and `"` escaped and other characters outside printable ASCII
 * (and commas) as \u00XX.
 */
static void print_alt_name(FILE *out, const unsigned char *name, size_t len) {
  bool safe = true;
  for (size_t i = 0; i < len && safe; i++) {
    safe = name[i] >= ' ' && name[i] <= '~' && name[i] != '"' && name[i] != '\\' && name[i] != ',' && name[i] != '\'';
  }
  if (safe) {
    fwrite(name, 1, len, out);
    return;
  }
  fputc('"', out);
  for (size_t i = 0; i < len; i++) {
    if (name[i] == '\\' || name[i] == '"') {
      fprintf(out, "\\%c", name[i]);
    } else if (name[i] >= ' ' && name[i] <= '~' && name[i] != ',') {
      fputc(name[i], out);
    } else {
      fprintf(out, "\\u00%02x", name[i]);
    }
  }
  fputc('"', out);
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
      print_alt_name(out, name->buf.p, name->buf.len);
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

/*
 * A directory string's text as UTF-8, malloc'd, its length in *len: a BMPString's UCS-2
 * and a UniversalString's UCS-4 decoded, as OpenSSL decodes them before Node sees a common
 * name, other kinds' bytes as they are; NULL for a BMPString or UniversalString OpenSSL
 * refuses (a length not a whole number of characters, a surrogate, a code point past
 * U+10FFFF).
 */
static unsigned char *directory_text(const mbedtls_x509_buf *value, size_t *len) {
  size_t unit = value->tag == MBEDTLS_ASN1_BMP_STRING ? 2 : value->tag == MBEDTLS_ASN1_UNIVERSAL_STRING ? 4 : 1;
  if (value->len % unit != 0) {
    return NULL;
  }
  unsigned char *text = checked(malloc(value->len * 4 + 1)), *end = text;
  for (size_t i = 0; i + unit <= value->len; i += unit) {
    uint32_t c = value->p[i];
    for (size_t j = 1; j < unit; j++) {
      c = c << 8 | value->p[i + j];
    }
    if (unit > 1 && ((c >= 0xD800 && c <= 0xDFFF) || c > 0x10FFFF)) {
      free(text);
      return NULL;
    }
    if (unit == 1 || c < 0x80) {
      *end++ = (unsigned char)c;
    } else if (c < 0x800) {
      *end++ = (unsigned char)(0xC0 | c >> 6), *end++ = (unsigned char)(0x80 | (c & 0x3F));
    } else if (c < 0x10000) {
      *end++ = (unsigned char)(0xE0 | c >> 12), *end++ = (unsigned char)(0x80 | (c >> 6 & 0x3F)), *end++ = (unsigned char)(0x80 | (c & 0x3F));
    } else {
      *end++ = (unsigned char)(0xF0 | (c >> 18 & 0x07)), *end++ = (unsigned char)(0x80 | (c >> 12 & 0x3F));
      *end++ = (unsigned char)(0x80 | (c >> 6 & 0x3F)), *end++ = (unsigned char)(0x80 | (c & 0x3F));
    }
  }
  *len = (size_t)(end - text);
  return text;
}

/* Whether a common name of crt names host: *some if it has any, printed to out. */
static bool common_name_names(const mbedtls_x509_crt *crt, const char *host, size_t host_len, bool *some, FILE *out) {
  for (const mbedtls_x509_name *name = &crt->subject; name != NULL; name = name->next) {
    if (MBEDTLS_OID_CMP(MBEDTLS_OID_AT_CN, &name->oid) == 0 && name->val.len > 0) {
      size_t len = 0;
      unsigned char *text = directory_text(&name->val, &len); /* NULL: names nothing */
      fputs(*some ? "," : "", out);
      fwrite(text, 1, len, out);
      *some = true;
      bool named = text != NULL && pattern_names(text, len, host, host_len);
      free(text);
      if (named) {
        return true;
      }
    }
  }
  return false;
}

/*
 * Whether crt names the server's host, as Node's tls.checkServerIdentity decides: an IP
 * address by the certificate's IP addresses alone, a DNS name by its DNS names or, if it
 * has none, its common names. If not, *reason (malloc'd, *len bytes) is Node's reason.
 */
static bool names_host(const pts_tls *tls, const mbedtls_x509_crt *crt, char **reason, size_t *len) {
  size_t common_len = 0;
  char *common = NULL;
  bool some_names = false, some_common = false, named = false;
  FILE *out = checked(open_memstream(reason, len)), *common_out = checked(open_memstream(&common, &common_len));
  if (tls->address_len > 0) {
    named = names_address(crt, tls->address, tls->address_len);
    fprintf(out, "IP: %s is not in the cert's list: ", tls->host);
    print_names(out, crt, true);
  } else if ((named = dns_name_names(crt, tls->host, tls->host_len, &some_names)) || some_names) {
    fprintf(out, "Host: %s. is not in the cert's altnames: ", tls->host);
    print_names(out, crt, false);
  } else if ((named = common_name_names(crt, tls->host, tls->host_len, &some_common, common_out)) || some_common) {
    fflush(common_out);
    fprintf(out, "Host: %s. is not cert's CN: ", tls->host);
    fwrite(common, 1, common_len, out);
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

/* ---------------------------------------------------------------- the server's chain */

/* OIDs of Server Gated Crypto, Netscape's and Microsoft's, which OpenSSL takes for serverAuth. */
#define OID_NETSCAPE_SGC "\x60\x86\x48\x01\x86\xf8\x42\x04\x01"
#define OID_MICROSOFT_SGC "\x2b\x06\x01\x04\x01\x82\x37\x0a\x03\x03"

/*
 * Whether crt's extended key usage lets it serve TLS or issue for servers, as OpenSSL's
 * purpose check decides: it has none, or it lists serverAuth or Server Gated Crypto
 * (anyExtendedKeyUsage alone doesn't do). mbedTLS checks only the server's own, and takes
 * anyExtendedKeyUsage.
 */
static bool usage_serves_tls(const mbedtls_x509_crt *crt) {
  if (!mbedtls_x509_crt_has_ext_type(crt, MBEDTLS_X509_EXT_EXTENDED_KEY_USAGE)) {
    return true;
  }
  for (const mbedtls_x509_sequence *usage = &crt->ext_key_usage; usage != NULL && usage->buf.p != NULL; usage = usage->next) {
    if (MBEDTLS_OID_CMP(MBEDTLS_OID_SERVER_AUTH, &usage->buf) == 0 || MBEDTLS_OID_CMP(OID_NETSCAPE_SGC, &usage->buf) == 0 ||
        MBEDTLS_OID_CMP(OID_MICROSOFT_SGC, &usage->buf) == 0) {
      return true;
    }
  }
  return false;
}

/*
 * Whether crt has a name constraints extension. mbedTLS doesn't parse one: it fails a
 * certificate whose extension is critical and ignores one that isn't, so such a CA could
 * issue for any name.
 */
static bool has_name_constraints(const mbedtls_x509_crt *crt) {
  unsigned char *p = crt->v3_ext.p, *end = p + crt->v3_ext.len;
  size_t len;
  if (p == NULL || mbedtls_asn1_get_tag(&p, end, &len, MBEDTLS_ASN1_CONSTRUCTED | MBEDTLS_ASN1_SEQUENCE) != 0) {
    return false;
  }
  end = p + len;
  while (p < end) {
    unsigned char *extension_end;
    mbedtls_asn1_buf oid;
    if (mbedtls_asn1_get_tag(&p, end, &len, MBEDTLS_ASN1_CONSTRUCTED | MBEDTLS_ASN1_SEQUENCE) != 0) {
      return false;
    }
    extension_end = p + len;
    if (mbedtls_asn1_get_tag(&p, extension_end, &oid.len, MBEDTLS_ASN1_OID) != 0) {
      return false;
    }
    oid.p = p;
    if (MBEDTLS_OID_CMP(MBEDTLS_OID_NAME_CONSTRAINTS, &oid) == 0) {
      return true;
    }
    p = extension_end;
  }
  return false;
}

/* Whether child names another key as its issuer's than parent's: its authority key identifier against parent's subject key identifier, where both have one. */
static bool other_issuer_key(const mbedtls_x509_crt *child, const mbedtls_x509_crt *parent) {
  const mbedtls_x509_buf *authority = &child->authority_key_id.keyIdentifier, *subject = &parent->subject_key_id;
  return authority->len > 0 && subject->len > 0 && (authority->len != subject->len || memcmp(authority->p, subject->p, subject->len) != 0);
}

/* Its validity flags, MBEDTLS_X509_BADCERT_EXPIRED or …_FUTURE, as mbedTLS checks a chain's. */
static uint32_t validity_of(const mbedtls_x509_crt *crt) {
  return (mbedtls_x509_time_is_past(&crt->valid_to) ? MBEDTLS_X509_BADCERT_EXPIRED : 0) |
         (mbedtls_x509_time_is_future(&crt->valid_from) ? MBEDTLS_X509_BADCERT_FUTURE : 0);
}

/*
 * Why mbedTLS didn't take a certificate among candidates that names crt's issuer, by name
 * and key identifier, as its issuer, crt being at depth (mbedTLS's checks in
 * x509_crt_check_parent and x509_crt_find_parent_in), with that certificate's validity in
 * *validity; a trusted one with no other fault was refused for its signature.
 * MBEDTLS_PRIVATE: mbedTLS keeps the basic constraints to itself.
 */
static refusal refusal_among(const mbedtls_x509_crt *crt, const mbedtls_x509_crt *candidates, int depth, bool trusted, uint32_t *validity) {
  for (const mbedtls_x509_crt *other = candidates; other != NULL && other->raw.p != NULL; other = other->next) {
    if (other == crt || other->subject_raw.len != crt->issuer_raw.len || memcmp(other->subject_raw.p, crt->issuer_raw.p, crt->issuer_raw.len) != 0 ||
        other_issuer_key(crt, other)) {
      continue;
    }
    *validity = validity_of(other);
    if (!other->MBEDTLS_PRIVATE(ca_istrue) && other->version >= 3) {
      return REFUSED_NOT_CA;
    }
    if (mbedtls_x509_crt_check_key_usage(other, MBEDTLS_X509_KU_KEY_CERT_SIGN) != 0) {
      return REFUSED_NO_CERT_SIGN;
    }
    if (other->MBEDTLS_PRIVATE(max_pathlen) > 0 && other->MBEDTLS_PRIVATE(max_pathlen) < 1 + depth) {
      return REFUSED_PATH_LENGTH;
    }
    if (trusted) {
      return REFUSED_SIGNATURE;
    }
  }
  *validity = 0;
  return REFUSED_NONE;
}

/*
 * mbedTLS calls this for each certificate of the chain it built from the server's, from
 * the top down to the server's own (depth 0), with what it found wrong in flags, which
 * this adds to: every CA's extended key usage, and name constraints (refused, as they
 * can't be checked); at the server's own, the names by Node's rules instead of mbedTLS's,
 * which take no common name when there are IP addresses and take a wildcard of a
 * top-level domain. What it saw explains the failure (verify_failure).
 */
static int on_verify(void *data, mbedtls_x509_crt *crt, int depth, uint32_t *flags) {
  pts_tls *tls = data;
  if (tls->chain_length == 0) {
    tls->chain_length = depth + 1;
    tls->top_self_issued = crt->issuer_raw.len == crt->subject_raw.len && memcmp(crt->issuer_raw.p, crt->subject_raw.p, crt->subject_raw.len) == 0;
    if ((*flags & MBEDTLS_X509_BADCERT_NOT_TRUSTED) && !tls->top_self_issued) {
      tls->refused = refusal_among(crt, crt->next, depth, false, &tls->refused_validity);
      if (tls->refused == REFUSED_NONE) {
        tls->refused = refusal_among(crt, &client_setup.roots, depth, true, &tls->refused_validity);
      }
    }
  }
  if (!usage_serves_tls(crt)) {
    *flags |= MBEDTLS_X509_BADCERT_EXT_KEY_USAGE;
  }
  if (depth > 0 && has_name_constraints(crt)) {
    *flags |= MBEDTLS_X509_BADCERT_OTHER;
    tls->constrained = true;
  }
  if (depth == 0) {
    tls->leaf_key_weak = mbedtls_pk_get_type(&crt->pk) == MBEDTLS_PK_RSA && mbedtls_pk_get_bitlen(&crt->pk) < mbedtls_x509_crt_profile_default.rsa_min_bitlen;
    free(tls->mismatch);
    tls->mismatch = NULL;
    *flags &= ~(uint32_t)MBEDTLS_X509_BADCERT_CN_MISMATCH;
    if (!names_host(tls, crt, &tls->mismatch, &tls->mismatch_len)) {
      *flags |= MBEDTLS_X509_BADCERT_CN_MISMATCH;
    }
    tls->names_checked = true;
  }
  if (depth < MBEDTLS_X509_MAX_VERIFY_CHAIN_SIZE) {
    tls->found[depth] = *flags;
    tls->self_issued[depth] = crt->issuer_raw.len == crt->subject_raw.len && memcmp(crt->issuer_raw.p, crt->subject_raw.p, crt->subject_raw.len) == 0;
    tls->other_key[depth] = tls->above != NULL && other_issuer_key(crt, tls->above);
  }
  tls->above = depth > 0 ? crt : NULL;
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
  *failure = psa_failure();
  if (*failure != PTS_VALUE_ABSENT) {
    return NULL;
  }
  uv_once(&client_once, make_client_setup);
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

/* ---------------------------------------------------------------- servers (D-224) */

struct pts_tls_identity {
  mbedtls_ssl_config config;
  mbedtls_x509_crt chain;
  mbedtls_pk_context key;
};

void pts_tls_identity_free(pts_tls_identity *identity) {
  mbedtls_ssl_config_free(&identity->config);
  mbedtls_x509_crt_free(&identity->chain);
  mbedtls_pk_free(&identity->key);
  free(identity);
}

void pts_tls_forget(char *bytes, size_t len) {
  mbedtls_platform_zeroize(bytes, len);
  free(bytes);
}

/*
 * Why mbedTLS can't take the certificate chain and key, as serve's Error: PTS_VALUE_ABSENT
 * if it can. A chain of several certificates of which some don't parse is refused too.
 */
static pts_value identity_failure(pts_tls_identity *identity, const char *cert, size_t cert_len, const char *key, size_t key_len,
                                  const pts_string *passphrase) {
  int error = mbedtls_x509_crt_parse(&identity->chain, (const unsigned char *)cert, cert_len);
  if (error > 0) {
    return error_of("serve's tls cert: a certificate in it can't be parsed");
  }
  if (error != 0) {
    return described_after("serve's tls cert: ", error);
  }
  error = mbedtls_pk_parse_key(&identity->key, (const unsigned char *)key, key_len, passphrase != NULL ? (const unsigned char *)passphrase->bytes : NULL,
                               passphrase != NULL ? passphrase->byte_len : 0, mbedtls_psa_get_random, MBEDTLS_PSA_RANDOM_STATE);
  if (error != 0) {
    return described_after("serve's tls key: ", error);
  }
  if (mbedtls_pk_check_pair(&identity->chain.pk, &identity->key, mbedtls_psa_get_random, MBEDTLS_PSA_RANDOM_STATE) != 0) {
    return error_of("serve's tls key doesn't match the certificate");
  }
  mbedtls_ssl_config *config = &identity->config;
  error = mbedtls_ssl_config_defaults(config, MBEDTLS_SSL_IS_SERVER, MBEDTLS_SSL_TRANSPORT_STREAM, MBEDTLS_SSL_PRESET_DEFAULT);
  if (error == 0) {
    error = mbedtls_ssl_conf_alpn_protocols(config, ALPN);
  }
  if (error == 0) {
    error = mbedtls_ssl_conf_own_cert(config, &identity->chain, &identity->key);
  }
  if (error != 0) {
    return described_after("serve's tls: ", error);
  }
  mbedtls_ssl_conf_rng(config, mbedtls_psa_get_random, MBEDTLS_PSA_RANDOM_STATE);
  return PTS_VALUE_ABSENT;
}

pts_tls_identity *pts_tls_identity_new(const char *cert, size_t cert_len, const char *key, size_t key_len, const pts_string *passphrase, pts_value *failure) {
  *failure = psa_failure();
  if (*failure != PTS_VALUE_ABSENT) {
    return NULL;
  }
  pts_tls_identity *identity = checked(malloc(sizeof *identity));
  mbedtls_ssl_config_init(&identity->config);
  mbedtls_x509_crt_init(&identity->chain);
  mbedtls_pk_init(&identity->key);
  *failure = identity_failure(identity, cert, cert_len, key, key_len, passphrase);
  if (*failure != PTS_VALUE_ABSENT) {
    pts_tls_identity_free(identity);
    return NULL;
  }
  return identity;
}

/* Room for a private key in DER: an RSA key of 16,384 bits takes about 9.3 KB. */
enum { KEY_DER_MAX = 16 * 1024 };

char *pts_tls_identity_key(const pts_tls_identity *identity, size_t *len) {
  unsigned char *der = checked(malloc(KEY_DER_MAX));
  int written = mbedtls_pk_write_key_der(&identity->key, der, KEY_DER_MAX); /* at the buffer's end */
  if (written <= 0) {
    pts_tls_forget((char *)der, KEY_DER_MAX);
    return NULL;
  }
  memmove(der, der + KEY_DER_MAX - written, (size_t)written);
  mbedtls_platform_zeroize(der + written, KEY_DER_MAX - (size_t)written);
  *len = (size_t)written;
  return (char *)der;
}

pts_tls *pts_tls_server(pts_tls_identity *identity, pts_value *failure) {
  pts_tls *tls = checked(calloc(1, sizeof *tls + 1));
  tls->server = true;
  mbedtls_ssl_init(&tls->ssl);
  int error = mbedtls_ssl_setup(&tls->ssl, &identity->config);
  if (error != 0) {
    *failure = described(error);
    pts_tls_free(tls);
    return NULL;
  }
  mbedtls_ssl_set_bio(&tls->ssl, tls, send_out, receive_in, NULL);
  return tls;
}

/* ---------------------------------------------------------------- steps */

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
  if (status == PTS_TLS_DONE && !tls->server && !tls->names_checked) {
    tls->error = MBEDTLS_ERR_X509_FATAL_ERROR; /* never: every handshake verifies the server's certificate, through on_verify */
    return PTS_TLS_FAILED;
  }
  return status;
}

pts_tls_status pts_tls_write(pts_tls *tls, const char *bytes, size_t len) {
  if (tls->error != 0) {
    return PTS_TLS_FAILED; /* nothing follows a fatal alert (RFC 8446 §6.2); mbedTLS would seal it */
  }
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

pts_tls_status pts_tls_close(pts_tls *tls) {
  if (tls->error != 0) {
    return PTS_TLS_FAILED;
  }
  if (tls->closed) {
    return PTS_TLS_DONE; /* mbedtls_ssl_close_notify would write a second one */
  }
  tls->closed = true;
  return status_of(tls, mbedtls_ssl_close_notify(&tls->ssl));
}

pts_tls_status pts_tls_read(pts_tls *tls, char *out, size_t cap, size_t *got) {
  int result = mbedtls_ssl_read(&tls->ssl, (unsigned char *)out, cap);
  if (result > 0) {
    *got = (size_t)result;
    return PTS_TLS_DONE;
  }
  return result == 0 ? PTS_TLS_CLOSED : status_of(tls, result);
}
