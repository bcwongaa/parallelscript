# H33a review rounds

## Round 1 (verbatim)

Child: `reviewer` (claude-opus-5-5, xhigh), read-only, on snapshot 1 (`/tmp/h33/snap1`: `37a9ce909` plus the change, the implementation later committed as `afbfd1c87`). Probes: [h33a-review1-probes.tar.gz](h33a-review1-probes.tar.gz). The child's final message follows, unedited except that HTML entities are written as their characters.

---

# H33a review, round 1: HTTPS client for `fetch`

**VERDICT: NOT READY.** pts accepts certificate chains that Node, OpenSSL and browsers reject (R1-F1, R1-F2). The rest of the slice held up well. The snapshot at `/tmp/h33/snap1` is untouched; probes are in `/tmp/h33/review1/` and `/tmp/h33/review1-probes.tar.gz`.

## What I checked and found sound
- **Focused tests pass.** `TestFetchHTTPS{MatchesNode,TrustStore,EveryLoop,PublicSite}` with `PTS_REQUIRE_NODE=1`, `GOMAXPROCS=2 -p 1`: `ok … 20.497s` (`/tmp/h33/review1/focused1.log`).
- **Wildcard name matching equals Node's.** I compiled `pattern_names` and its helpers verbatim from `tls.c` and fuzzed them against Node 24's `tls.checkServerIdentity` (DNS altname path): 4 seeds × 200k cases, **0 differences**. The cases covered wildcards, `xn--` in either case, empty labels, trailing dots, case, NUL, 0x7F, bytes ≥0x80, space and tab (`fuzz/harness.c`, `fuzz/fuzz.mjs`).
- **Same results as Node** (`p1`, `p4`) for:
  - IPv6 mismatch messages (`IP: ::1 is not in the cert's list: 127.0.0.1, ::2`);
  - a CN-only wildcard (accepted) and the same CN two labels deep (rejected, same text);
  - several CNs; uppercase and trailing-dot SANs;
  - DNS+IPv6 and email SAN messages;
  - a trusted self-signed leaf, an expired trusted root, a leaf that is a CA, a leaf without EKU;
  - HEAD/204/304 and a 10 MB POST over TLS;
  - credentials on https→https to another port (dropped) and to the same origin written in capitals (kept).
- **Handshake behaviour.**
  - OpenSSL `s_server` over TLS 1.3 sends two NewSessionTickets; pts gets 200.
  - Fetching by IP over TLS 1.2 and 1.3 reaches pts's own name check and gives Node's exact message; there is no "verification without hostname" failure.
  - mbedTLS has ticket signalling off by default, so tickets are skipped silently (`ssl_msg.c:5788-5810`).
  - In the code I found no path that writes the request before `mbedtls_ssl_handshake` returns 0. The handshake fails under `VERIFY_REQUIRED` whenever any flag is set. Clearing `CN_MISMATCH` only at depth 0 (the server's own certificate) is correct: mbedTLS only ever sets that flag there.
- **Memory and threads.**
  - With aborts at random stages against good, stalled, held, junk, untrusted and wrong-host servers, ASan+UBSan builds ran on 1 and 4 loops with LSan on (`p3`; ASan report text in `p3/san*.err`). No ASan or UBSan errors, and no leaks with tls/fetch/mbedtls/psa frames. The only leaks reported are the loops' own structures at exit.
  - TSan on 4 loops (`GC_DONT_GC=1`) reported nothing. This only covers the runtime, because the vendored libraries are built uninstrumented.
  - Reading the code, the order of write-cancel and `on_closed_free`, ownership of `sending` and `tls->out`, the uses of `c->sealed` and `c->in`, every failure path of `start_secure`, the `uv_once` setup, and the shared root and config contexts all look safe. ECP uses static comb tables, RSA public operations lock, and PSA's RNG locks.
- **System roots.** All 120 roots in `/etc/ssl/certs/ca-certificates.crt` parse with the runtime's mbedTLS build (`roots/roots.c`).

## Findings

### R1-F1 (high): name constraints on intermediate CAs are ignored. Verified.
- **Where:** `tls.c:413-430` (`on_verify` only checks names at depth 0). The cause is mbedTLS 3.6.7 itself (`x509_crt.c:1058-1066`): a non-critical nameConstraints extension is skipped, and a critical one fails the certificate's parse.
- **Repro:** `/tmp/h33/review1/run_lab_probes.sh`. It builds the `lab/main.go` servers and runs `p1/probe1` under Node and pts with `NODE_EXTRA_CA_CERTS`.

| Server | Node | pts |
|---|---|---|
| `nc-noncrit-permitted-other` | `UNSPECIFIED \| permitted subtree violation` | `200` |
| `nc-noncrit-excluded` | `UNSPECIFIED \| excluded subtree violation` | `200` |
| `nc-noncrit-ip` (IP range) | `UNSPECIFIED \| permitted subtree violation` | `200` |
| `nc-crit-permitted-ok` (a valid chain) | `200` | `undefined \| X509 - The extension tag or value is invalid : … (mbedTLS -0x2562)` |

- **Why it matters:** technically constrained sub-CAs are exempt from audits precisely because of these constraints. Under pts, the holder of one can issue for any name. Valid constrained chains marked critical (as RFC 5280 requires) fail with an opaque error.
- **Not in any doc or test.** The fix should at least fail closed when a non-leaf certificate carries id-ce-nameConstraints (2.5.29.30, readable from `crt->v3_ext`), or implement the DNS and IP subtree checks, and test both cases.

### R1-F2 (medium): EKU on intermediates is ignored, and an anyEKU-only leaf is accepted. Verified.
- **Where:** `tls.c:413-430`. mbedTLS checks EKU only on the leaf (`ssl_tls.c:9886`), and treats anyExtendedKeyUsage as a match.
- **Repro:** the same probe.

| Server | Node | pts |
|---|---|---|
| `eku-inter-client` (intermediate EKU clientAuth only) | `INVALID_PURPOSE \| unsuitable certificate purpose` | `200` |
| `eku-inter-email` (intermediate EKU emailProtection only) | `INVALID_PURPOSE \| unsuitable certificate purpose` | `200` |
| `leaf-eku-any` | `INVALID_PURPOSE \| unsuitable certificate purpose` | `200` |

- **Why it matters:** an S/MIME-only or client-only sub-CA under a root trusted for websites can mint TLS server certificates that pts accepts.
- **Fix:** in `on_verify` at depth > 0, a certificate that has an EKU without serverAuth should get `MBEDTLS_X509_BADCERT_EXT_KEY_USAGE` (this already maps to Node's `INVALID_PURPOSE`). Treat anyEKU on its own as OpenSSL does. Add tests.

### R1-F3 (low–medium): Node codes and messages diverge outside the reproducer's matrix, and the doc that describes the gap is wrong. Verified.
- **Where:** `tls.c:174-201` (`verify_failure`); SPEC §7.1 "the last OpenSSL would find"; DECISIONS.md:2255 (D-223 Known gaps).

| Case | Node 24 | pts |
|---|---|---|
| Leaf with a bad signature from a sent intermediate | `CERT_SIGNATURE_FAILURE \| certificate signature failure` | `SELF_SIGNED_CERT_IN_CHAIN` |
| Leaf with a bad signature from the root | `CERT_SIGNATURE_FAILURE` | `UNABLE_TO_VERIFY_LEAF_SIGNATURE` |
| Path length exceeded | `PATH_LENGTH_EXCEEDED \| path length constraint exceeded` | `UNABLE_TO_GET_ISSUER_CERT_LOCALLY` |
| RSA-1024 leaf | `UNSPECIFIED \| EE certificate key too weak` | `EE_KEY_TOO_SMALL \| ee key too small` |
| RSA-1024 intermediate | `UNSPECIFIED \| CA certificate key too weak` | `EE_KEY_TOO_SMALL` |
| SHA-1-signed leaf | `UNSPECIFIED \| CA signature digest algorithm too weak` | `CA_MD_TOO_WEAK \| ca md too weak` |
| Intermediate without keyCertSign | `UNSPECIFIED \| key usage does not include certificate signing` | `INVALID_PURPOSE` |
| Lone untrusted leaf with a clientAuth-only EKU (`p4`) | `UNABLE_TO_VERIFY_LEAF_SIGNATURE` | `INVALID_PURPOSE` |

The last row is a precedence issue: OpenSSL's `internal_verify` reports the leaf-signature error after the purpose check. D-223 says a refused issuer "(a bad signature, a path length) is still `INVALID_PURPOSE`". That is wrong for both examples, as the table shows.

### R1-F4 (low): TLS 1.3 KeyUpdate and TLS 1.2 server-initiated renegotiation fail the request; Node succeeds. Verified. Not documented.
- **Repro:** `/tmp/h33/review1/run_openssl_probes.sh`. It drives `openssl s_server` through a FIFO, sending `k`, `K` or `r` after the request.
- **pts:** `SSL - An unexpected message was received from our peer (mbedTLS -0x7700)` for `k` and `K`; `SSL - A fatal alert message was received from our peer (mbedTLS -0x7780)` for `r`.
- **Node:** `200 "ok"` in all three cases.
- **Cause:** mbedTLS 3.6 limitations (`ssl_msg.c:5789-5812`, `5874-5915`). They should be listed in D-223's Known gaps and §8.

### R1-F5 (low): name-check and message parity.
- **Where:** `tls.c:197`, `tls.c:311-362`.
- **Verified with `p1`:**
  - A CN encoded as BMPString `localhost`: Node accepts; pts rejects with `…is not cert's CN: ` and the CN cut off. Raw bytes are used, and `strlen` at `tls.c:197` stops at the first NUL.
  - SAN `localhost, DNS:x`: Node escapes it (`DNS:"localhost, DNS:x"`); pts prints it raw.
  - A P-256 leaf whose key usage is keyEncipherment only: Node gives 200; pts gives `INVALID_PURPOSE`.

### R1-F6 (low): a stalled handshake hangs `fetch`. Verified.
- **Repro:** `p6/hold.py`, a server that reads the ClientHello and holds for 40 s.
- **Node:** after 10 s, `UND_ERR_CONNECT_TIMEOUT | Connect Timeout Error … timeout: 10000ms`.
- **pts:** waits 40 s, then `ECONNRESET`. Against a server that never closes, it would wait forever.
- fetch has never had timeouts, but this new failure mode isn't documented.

### R1-F7 (low): docs and test comments.
- `ARCHITECTURE.md:458` says names are checked "by mbedTLS" for DNS hosts. That's stale: `on_verify` does it, by Node's rules.
- `https_test.go:795-798` overstates what ASan catches. mbedTLS and libuv are built with `vendoredFlags`, uninstrumented, so use-after-free or overreads inside mbedTLS go unseen.
- When `*.localhost` doesn't resolve, the wildcard lines are dropped, and the wildcard path then has no end-to-end coverage on that host. It does resolve on this machine.
- Not a defect: for `localhost.`, pts strips the dot from SNI and works, while Node sends `localhost.` and Go rejects it with a decode_error alert. pts is the RFC 6066-correct side, but §8 doesn't mention it.

VERDICT: NOT READY

## Round 2 (verbatim)

The same `reviewer`, resumed, read-only, on snapshot 2 (`/tmp/h33/snap2`: `afbfd1c87` plus round 1's fixes). Probes: [h33a-review2-probes.tar.gz](h33a-review2-probes.tar.gz).

---

# H33a review, round 2: fixes for the round-1 findings

**VERDICT: READY.** Every round-1 finding is either fixed and matches Node, or documented as a deliberate gap. What's left are four low-severity items: one small leniency in decoding common names (R2-F1), worth fixing cheaply but not blocking, and three gaps to document. Snapshot `/tmp/h33/snap2` is untouched; probes are in `/tmp/h33/review2/` and `/tmp/h33/review2-probes.tar.gz`.

## Re-run of the round-1 probes against snap2's CLI
Every round-1 probe now prints what Node prints, except the documented divergences below (`/tmp/h33/review2/run_lab_probes.log`).

- **Name constraints (R1-F1), fixed by failing closed.** All four constrained cases now fail: the three non-critical ones with `UNSPECIFIED | name constraints are not supported: a CA that has them is not trusted`, the critical one with mbedTLS's `-0x2562` parse error. A constrained root used directly fails the same way. This is documented. Node checks the constraints instead and takes the permitted ones.
- **EKU (R1-F2), fixed.** Intermediates for clientAuth or emailProtection only, and an anyEKU-only leaf, all give `INVALID_PURPOSE`, as Node does.
- **Error codes (R1-F3), fixed.** Every round-1 case now gives Node's code and text: bad signatures (both cases), path length, RSA-1024 server and CA keys, SHA-1, a CA without keyCertSign, and the lone untrusted leaf with a clientAuth-only EKU.
- **Name decoding and escaping (R1-F5), fixed.** BMPString and UniversalString common names are decoded, and the comma SAN is escaped as Node escapes it.
- **Still differs, as documented:** a P-256 leaf with keyEncipherment only (pts stricter), and the trailing-dot SNI.
- **OpenSSL `s_server` probes:** two session tickets work over TLS 1.3 and 1.2. KeyUpdate (`k`, `K`) and TLS 1.2 renegotiation (`r`) still fail, as now documented.

## New checks this round
- **Focused tests pass.** `TestFetchHTTPS{MatchesNode,NameConstraints,TrustStore}`, run read-only from snap2 with `PTS_REQUIRE_NODE=1` and `GOMAXPROCS=2 -p 1`: `ok … 23.539s` (`focused2.log`).
- **Precedence combinations** (`lab2/main.go`, `c1/combos.pts`; replay with `run_combos.sh`). pts matches Node on all of these:

| Combination | Result for both |
|---|---|
| Expired leaf with a bad signature from the sent intermediate | `CERT_HAS_EXPIRED` |
| Expired intermediate, leaf with a bad signature | `CERT_SIGNATURE_FAILURE` |
| Intermediate whose signature by the root is bad | `CERT_SIGNATURE_FAILURE` |
| EKU intermediate from an untrusted root, root sent or not | `INVALID_PURPOSE` |
| EKU intermediate + leaf with a bad signature | `CERT_SIGNATURE_FAILURE` |
| EKU intermediate + expired leaf | `CERT_HAS_EXPIRED` |
| Non-CA issuer + expired leaf; no-keyCertSign intermediate + expired leaf | `CERT_HAS_EXPIRED` |
| Fake root with the trusted root's name, sent by the server | `SELF_SIGNED_CERT_IN_CHAIN` |
| Trusted RSA-1024 root | `CA certificate key too weak` |
| Weak leaf key + expired | `CERT_HAS_EXPIRED` |
| Lone untrusted leaf with a weak key | `UNABLE_TO_VERIFY_LEAF_SIGNATURE` |
| Untrusted intermediate sent + weak leaf key | `EE certificate key too weak` |
| SHA-1-signed intermediate | `CA signature digest algorithm too weak` |
| Root sent before the intermediate | 200 |

- **Escaping matches Node exactly** for SAN bytes 0xE9, 0x01, `"`, `\`, `'` and plain ASCII.
- **Sanitizers.** The combos built with ASan and UBSan (via the overlay builder, snap2's runtime) on 1 and 4 loops give output identical to the plain build. No ASan or UBSan errors, and no leaks with tls/x509/psa frames.
- **System roots.** None of the 120 in `/etc/ssl/certs/ca-certificates.crt` has name constraints or an EKU, so D-223's "no root has them" holds.
- **Public sites.** 15 sites were fetched over TLS by both pts and Node, including example.com, google, github, cloudflare, wikipedia, letsencrypt, microsoft, apple, digicert, gov.uk and globalsign. TLS worked everywhere. The only differences were at the HTTP level (amazon returned 405 to pts's HEAD) or Node timing out on two sites; none involved certificates.
- **Code review of the new parts.**
  - `has_name_constraints` (`tls.c:534-558`) only walks extensions mbedTLS has already validated, using its bounds-checked `get_tag`. A v1 certificate has no extensions to walk (`v3_ext.p` is NULL), and a v1/v2 certificate carrying extensions fails mbedTLS's parse. It is safe.
  - `found[]` writes are guarded by `depth < MBEDTLS_X509_MAX_VERIFY_CHAIN_SIZE`, and `chain_length` can't exceed that size.
  - `directory_text`'s buffer (`len*4+1`) is always large enough. Decoding can't turn a code point of 0x80 or above into an ASCII byte, so no ASCII name can be spoofed.
  - `refusal_among`'s path-length test equals mbedTLS's `1 + path_cnt - self_cnt` except for `self_cnt`, which only matters with self-issued intermediates. It also doesn't label a non-CA v1 certificate sent by the server as `NOT_CA`. Both are negligible.
  - The `MBEDTLS_PRIVATE` field access works, but will break on the planned move to mbedTLS 4.x; flag it then.
  - The OIDs for both SGC usages are correct.

## Findings

### R2-F1 (low): malformed BMPString and UniversalString common names are decoded leniently and can match. Verified.
- **Where:** `tls.c:428-449` (`directory_text`). The condition `i + unit <= value->len` silently drops a trailing partial unit, and surrogates or code points above 0x10FFFF are encoded instead of rejected.
- **Repro:** `run_combos.sh`, cases `bmp-cn-odd-length` (BMP "localhost" plus one byte) and `universal-cn-bad-length`.

| Case | Node | pts |
|---|---|---|
| `bmp-cn-odd-length` | `ERR_SSL_BMPSTRING_IS_WRONG_LENGTH` (OpenSSL won't parse it) | `200` |
| `universal-cn-bad-length` | `ERR_SSL_UNIVERSALSTRING_IS_WRONG_LENGTH` | `200` |
| `universal-cn-invalid-code-points`, `bmp-cn-surrogates` | `ERR_SSL_ILLEGAL_CHARACTERS` | rejected, but with `ERR_TLS_CERT_ALTNAME_INVALID` |

- **Why only low:** it can only matter for CN-only certificates, since a certificate with any DNS SAN never consults the CN. Public CAs don't issue CN-only certificates.
- **Fix:** treat the CN as unusable (no match) when the length isn't a multiple of the unit, or when a code point is a surrogate or above 0x10FFFF.

### R2-F2 (low): a chain sent with two intermediates out of order fails; Node accepts it. Verified. Not documented.
- **Where:** an mbedTLS 3.6 limitation. `x509_crt_find_parent` only searches the certificates after the child in the order the server sent them. Not caused by round-2 code.
- **Repro:** case `chain-misordered` (root → A → B → leaf, sent as `[leaf, A, B]`). Node gives 200; pts gives `UNABLE_TO_GET_ISSUER_CERT_LOCALLY`. The in-order chain and root-before-intermediate both give 200.
- RFC 8446 §4.4.2 asks clients to be prepared for arbitrary orderings. Add it to D-223's Known gaps and §8.

### R2-F3 (low): an intermediate in the trust store is a trust anchor in pts but not in Node. Verified. Not documented.
- **Repro:** case `partial-chain-trusted-inter`, where `NODE_EXTRA_CA_CERTS` holds only the intermediate. Node gives `UNABLE_TO_GET_ISSUER_CERT | unable to get issuer certificate`; pts gives 200.
- pts is looser than Node here, but only for certificates the user put in the trust store explicitly. Go and browsers behave like pts. Document it in §8.

### R2-F4 (low): issuer selection by name only, and refused issuers' validity unexamined. Verified.
- **Same name, different key.** `refusal_among` (`tls.c:566-585`) and the bad-signature mapping (`tls.c:223`) match issuers by name alone. OpenSSL's issuer check also compares the certificate's AKID with the issuer's SKID.

| Case | Node | pts |
|---|---|---|
| `same-name-fake-root` (lone leaf from a CA named like a trusted root) | `UNABLE_TO_VERIFY_LEAF_SIGNATURE` | `CERT_SIGNATURE_FAILURE` |
| `same-name-fake-inter-real-sent` (server sends a same-named intermediate with another key, like an outdated one after a re-key) | `UNABLE_TO_VERIFY_LEAF_SIGNATURE` | `CERT_SIGNATURE_FAILURE` |

  pts's "signature failure" points at tampering when the actual problem is a missing or wrong intermediate.
- **Refused issuers' validity.** A refused issuer's own validity, and that of anything above it, is never examined:

| Case | Node | pts |
|---|---|---|
| `pathlen+expired-inter1` | `CERT_HAS_EXPIRED` | `PATH_LENGTH_EXCEEDED` |
| `notca-issuer-expired` | `CERT_HAS_EXPIRED` | `INVALID_PURPOSE` |

- D-223's Known gaps ("from the first certificate of that name", "not … certificate-by-certificate") only partly cover these.
- **Fix:** skip candidates whose SKID doesn't match the AKID's key identifier. The fields are available as `subject_key_id` and `authority_key_id.keyIdentifier`. Otherwise, document both.

### By design, not findings
- A name-constrained CA fails closed even when Node's verdict is a different error: `nc-inter+expired-leaf` gives the constraints message where Node says `CERT_HAS_EXPIRED`, and `nc-root-direct` fails where Node gives 200. Documented.

To replay: `run_lab_probes.sh`, `run_combos.sh`, `run_openssl_probes.sh` (all default to snap2's CLI; rebuilt binaries were removed to keep the archive small). The OpenSSL probes need `labdir.replay/ossl.*` copied into `labdir/` after a `run_lab_probes.sh` run.

VERDICT: READY
