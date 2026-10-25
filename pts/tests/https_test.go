package tests

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

// ---------------------------------------------------------------- throwaway certificates

// authority is a CA's certificate and key, which sign other certificates.
type authority struct {
	cert *x509.Certificate
	key  crypto.Signer
}

// newKey makes a private key of kind: "p256", "p384", "rsa" (2048 bits) or "rsa1024".
func newKey(t *testing.T, kind string) crypto.Signer {
	t.Helper()
	var key crypto.Signer
	var err error
	switch kind {
	case "p256":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "p384":
		key, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "rsa":
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	case "rsa1024":
		key, err = rsa.GenerateKey(rand.Reader, 1024)
	default:
		t.Fatalf("no key of kind %q", kind)
	}
	assert.NilError(t, err)
	return key
}

// certifyDER makes the DER of the certificate template describes, for key, signed by
// issuer, or by key itself if issuer is nil. It may be one Go can't parse back.
func certifyDER(t *testing.T, template *x509.Certificate, key crypto.Signer, issuer *authority) []byte {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	assert.NilError(t, err)
	template.SerialNumber = serial
	parent, signer := template, key
	if issuer != nil {
		parent, signer = issuer.cert, issuer.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, key.Public(), signer)
	assert.NilError(t, err)
	return der
}

// certify makes the certificate template describes, for key, signed by issuer, or by key itself if issuer is nil.
func certify(t *testing.T, template *x509.Certificate, key crypto.Signer, issuer *authority) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(certifyDER(t, template, key, issuer))
	assert.NilError(t, err)
	return cert
}

// newAuthority makes a CA called name with a key of kind, signed by issuer (an intermediate
// CA) or by itself (a root), valid from an hour ago for a day unless edit changes that.
func newAuthority(t *testing.T, name string, kind string, issuer *authority, edit func(*x509.Certificate)) *authority {
	t.Helper()
	key := newKey(t, kind)
	now := time.Now()
	template := &x509.Certificate{
		Subject:               pkix.Name{CommonName: name, Organization: []string{"ParallelScript tests"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	if edit != nil {
		edit(template)
	}
	return &authority{certify(t, template, key, issuer), key}
}

// serverCert makes a server's certificate, signed by issuer (by itself if nil), and the
// chain the server sends: the certificate, then sent. By default the certificate is for
// localhost, 127.0.0.1 and ::1, valid from an hour ago for a day, for TLS servers, with a
// P-256 key; edit changes that.
func serverCert(t *testing.T, issuer *authority, kind string, edit func(*x509.Certificate), sent ...*x509.Certificate) tls.Certificate {
	t.Helper()
	key := newKey(t, kind)
	now := time.Now()
	template := &x509.Certificate{
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if strings.HasPrefix(kind, "rsa") {
		template.KeyUsage |= x509.KeyUsageKeyEncipherment
	}
	if edit != nil {
		edit(template)
	}
	chain := tls.Certificate{Certificate: [][]byte{certifyDER(t, template, key, issuer)}, PrivateKey: key}
	for _, cert := range sent {
		chain.Certificate = append(chain.Certificate, cert.Raw)
	}
	return chain
}

// broken is chain with its first certificate's signature changed, so that its issuer's key doesn't verify it.
func broken(chain tls.Certificate) tls.Certificate {
	der := slices.Clone(chain.Certificate[0])
	der[len(der)-1] ^= 1
	chain.Certificate = append([][]byte{der}, chain.Certificate[1:]...)
	return chain
}

// commonNameSubject is a subject whose common name is the string of ASN.1 tag holding bytes.
func commonNameSubject(t *testing.T, tag int, bytes []byte) []byte {
	t.Helper()
	value := asn1.RawValue{Class: asn1.ClassUniversal, Tag: tag, Bytes: bytes}
	subject, err := asn1.Marshal(pkix.RDNSequence{{{Type: asn1.ObjectIdentifier{2, 5, 4, 3}, Value: value}}})
	assert.NilError(t, err)
	return subject
}

// bmpString is name in a BMPString's UTF-16, which OpenSSL decodes before Node reads a common name.
func bmpString(name string) []byte {
	var units []byte
	for _, unit := range utf16.Encode([]rune(name)) {
		units = append(units, byte(unit>>8), byte(unit))
	}
	return units
}

// writePEM writes certs to a new PEM file in dir and returns its path.
func writePEM(t *testing.T, dir string, name string, certs ...*x509.Certificate) string {
	t.Helper()
	var out []byte
	for _, cert := range certs {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
	}
	path := filepath.Join(dir, name)
	assert.NilError(t, os.WriteFile(path, out, 0o644))
	return path
}

// ---------------------------------------------------------------- the servers

// tlsLab is the servers the TLS tests fetch from, each named (the programs get their base
// URLs as name=url arguments), the CA files that trust them or not, and the state of the
// servers that hold, drop or refuse connections, which the programs ask the plain server
// about, so that they know a failure came from the stage it was meant to.
type tlsLab struct {
	args      []string
	trusted   string   // a PEM file of the CAs the servers' good certificates come from
	untrusted string   // a PEM file of a CA that signs some servers' certificates, which no test trusts
	absent    []string // the labels of fetch_tls.pts's lines for servers this host can't have
	hellos    sync.Map
	mu        sync.Mutex
	states    map[string]string // what happened to the last connection of a server whose state the programs ask
}

// statePaths are the paths the plain server answers with the state of another server, or
// of those whose certificates don't verify (/unverified: the names of those a request reached).
var statePaths = []string{"/holds", "/closes", "/plain-as-https", "/tls10", "/requires-client-cert", "/stalled", "/unverified"}

// state is what happened to the last connection of the server that path names ("none" before one).
func (lab *tlsLab) state(path string) string {
	lab.mu.Lock()
	defer lab.mu.Unlock()
	if state, ok := lab.states[path]; ok {
		return state
	}
	return "none"
}

func (lab *tlsLab) setState(path string, state string) {
	lab.mu.Lock()
	defer lab.mu.Unlock()
	lab.states[path] = state
}

// mustNotReach is mux for the server called name, whose certificate no client may accept:
// a request that reaches it, which the client sent after a handshake it should have
// failed, adds name to the state of /unverified.
func (lab *tlsLab) mustNotReach(name string, mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lab.mu.Lock()
		if reached := lab.states["/unverified"]; reached != "" {
			lab.states["/unverified"] = reached + ", " + name
		} else {
			lab.states["/unverified"] = name
		}
		lab.mu.Unlock()
		mux.ServeHTTP(w, r)
	})
}

// reset forgets the states, before a program runs.
func (lab *tlsLab) reset() {
	lab.mu.Lock()
	defer lab.mu.Unlock()
	lab.states = map[string]string{}
}

// name gives the server at base (a URL without a path) a name for the programs.
func (lab *tlsLab) name(name string, base string) {
	lab.args = append(lab.args, name+"="+base)
}

// base is the base URL of the server called name.
func (lab *tlsLab) base(name string) string {
	for _, arg := range lab.args {
		if found, ok := strings.CutPrefix(arg, name+"="); ok {
			return found
		}
	}
	panic("no server called " + name)
}

// listen listens on address and closes the listener when the test ends. On IPv6's
// loopback at a port the kernel picks, it holds that port on 127.0.0.1 as well, so that
// no other server has it there: where localhost resolves to ::1 first, as on GitHub's
// runners, a request to localhost at that port would try this listener first and reach
// the wrong server (CI run 40220044180).
func listen(t *testing.T, address string) net.Listener {
	t.Helper()
	if address == "[::1]:0" {
		return listenIPv6Loopback(t)
	}
	listener, err := net.Listen("tcp", address)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

// listenIPv6Loopback listens on [::1] at a port it also holds on 127.0.0.1, trying other
// ports while 127.0.0.1's is taken.
func listenIPv6Loopback(t *testing.T) net.Listener {
	t.Helper()
	for range 100 {
		listener, err := net.Listen("tcp", "[::1]:0")
		assert.NilError(t, err)
		held, err := net.Listen("tcp", "127.0.0.1:"+port(listener))
		if err == nil {
			t.Cleanup(func() { _ = listener.Close(); _ = held.Close() })
			return listener
		}
		_ = listener.Close()
	}
	t.Fatal("no port free on both loopbacks")
	return nil
}

// port is the port listener listens on.
func port(listener net.Listener) string {
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
}

// serve serves mux on listener, quietly (a handshake that fails is what many tests want).
func serve(t *testing.T, listener net.Listener, mux http.Handler) {
	server := &http.Server{Handler: mux, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
}

// serveTLS serves handler over TLS with cert on a new listener at address (127.0.0.1 if
// empty), edit changing the TLS configuration, and returns the listener's port. It
// remembers what each client offered in its hello, for /hello to answer, and, if
// statePath isn't empty, that a hello came, as statePath's state.
func (lab *tlsLab) serveTLS(t *testing.T, handler http.Handler, address string, cert tls.Certificate, edit func(*tls.Config), statePath string) string {
	t.Helper()
	if address == "" {
		address = "127.0.0.1:0"
	}
	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			lab.hellos.Store(hello.Conn.RemoteAddr().String(), offered(hello))
			if statePath != "" {
				lab.setState(statePath, "hello")
			}
			return nil, nil
		},
	}
	if edit != nil {
		edit(config)
	}
	listener := listen(t, address)
	serve(t, tls.NewListener(listener, config), handler)
	return port(listener)
}

// offered is what a client's hello offers that a client of the contract offers alike: the
// application protocols and the TLS versions (not the cipher suites, groups or signature
// algorithms, which OpenSSL and the TLS library differ in).
func offered(hello *tls.ClientHelloInfo) string {
	var versions []string
	for _, version := range hello.SupportedVersions {
		if version&0x0f0f != 0x0a0a { // not GREASE
			versions = append(versions, tls.VersionName(version))
		}
	}
	return fmt.Sprintf("offered alpn=%q versions=%q", hello.SupportedProtos, versions)
}

// handlers are the routes of every server of the lab, plain and TLS.
func (lab *tlsLab) handlers() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			fmt.Fprintf(w, "%s plain", r.Method)
			return
		}
		hello, _ := lab.hellos.Load(r.RemoteAddr)
		fmt.Fprintf(w, "%s sni=%q alpn=%q %s, %s", r.Method, r.TLS.ServerName, r.TLS.NegotiatedProtocol, tls.VersionName(r.TLS.Version), hello)
	})
	mux.HandleFunc("/n/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "n %s", strings.TrimPrefix(r.URL.Path, "/n/"))
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s tls=%v length=%d type=%q authorization=%q cookie=%q x-kept=%q body=%q", r.Method, r.TLS != nil, r.ContentLength,
			r.Header.Get("Content-Type"), r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.Header.Get("X-Kept"), body)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		status, err := strconv.Atoi(r.URL.Query().Get("status"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Location", r.URL.Query().Get("to"))
		w.WriteHeader(status)
	})
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		for _, part := range []string{"one ", "two ", "three"} {
			_, _ = io.WriteString(w, part)
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		piece := strings.Repeat("y", 64*1024)
		for range 319 {
			_, _ = io.WriteString(w, piece)
		}
		_, _ = io.WriteString(w, piece[:64*1024-3]+"end")
	})
	// hijacked writes raw itself, then ends the connection as end says: "close_notify"
	// (TLS's alert, then the TCP connection's end), "abruptly" (the TCP connection's end
	// alone) or "reset" (a TCP reset).
	hijacked := func(raw string, end string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				panic(err)
			}
			_, _ = io.WriteString(conn, raw)
			if secure, ok := conn.(*tls.Conn); ok && end != "close_notify" {
				conn = secure.NetConn()
			}
			if end == "reset" {
				_ = conn.(*net.TCPConn).SetLinger(0)
			}
			_ = conn.Close()
		}
	}
	const untilTheEnd = "HTTP/1.1 200 OK\r\ncontent-type: text/plain\r\n\r\nread until the end"
	const truncated = "HTTP/1.1 200 OK\r\ncontent-length: 100\r\n\r\nonly this"
	mux.HandleFunc("/close", hijacked(untilTheEnd, "close_notify"))
	mux.HandleFunc("/close-abruptly", hijacked(untilTheEnd, "abruptly"))
	mux.HandleFunc("/truncated", hijacked(truncated, "close_notify"))
	mux.HandleFunc("/truncated-abruptly", hijacked(truncated, "abruptly"))
	mux.HandleFunc("/reset", hijacked(truncated, "reset"))
	mux.HandleFunc("/stall", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		lab.setState("/stalled", "sent")
		select {
		case <-r.Context().Done():
			lab.setState("/stalled", "left")
		case <-time.After(20 * time.Second):
		}
	})
	for _, path := range statePaths {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, lab.state(path)) })
	}
	return mux
}

// What a raw server does after reading a client's first bytes.
const (
	closeAfterHello  = iota // closes the connection
	holdAfterHello          // holds it until the client closes it
	answerAfterHello        // answers as a plain HTTP server answers bytes that aren't HTTP, then closes
)

// serveRaw accepts TCP connections on 127.0.0.1 and reads what each client sends first,
// a ClientHello for a TLS client; then does what after says, recording each step as the
// state of path.
func (lab *tlsLab) serveRaw(t *testing.T, path string, after int) string {
	t.Helper()
	listener := listen(t, "127.0.0.1:0")
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				lab.setState(path, "connected")
				_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
				first := make([]byte, 16*1024)
				n, _ := conn.Read(first)
				if n == 0 || first[0] != 0x16 { // not a TLS handshake record
					lab.setState(path, fmt.Sprintf("read %q", first[:n]))
					return
				}
				lab.setState(path, "hello")
				switch after {
				case holdAfterHello:
					if _, err := io.Copy(io.Discard, conn); !isTimeout(err) {
						lab.setState(path, "left")
					}
				case answerAfterHello:
					_, _ = io.WriteString(conn, "HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\n\r\n400 Bad Request")
				}
			}()
		}
	}()
	return port(listener)
}

// isTimeout reports whether err is a read deadline passing.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// wildcardNames are the names under pts.localhost the wildcard certificates are tested
// with, which resolve to the loopback addresses where the C library resolves *.localhost
// (systemd's nss-myhostname or resolved).
var wildcardNames = []string{"a.pts.localhost", "a.b.pts.localhost", "pts.localhost", "a.localhost", "ab.pts.localhost"}

// resolvesToLoopback reports whether the C library resolves each of names to a loopback
// address, as pts's and Node's getaddrinfo would; Go's own resolver answers for
// *.localhost without asking it.
func resolvesToLoopback(names ...string) bool {
	for _, name := range names {
		out, err := exec.Command("getent", "ahostsv4", name).Output()
		if err != nil || !strings.HasPrefix(string(out), "127.") {
			return false
		}
	}
	return true
}

// startTLSLab makes the lab's certificates and starts its servers, named for fetch_tls.pts:
//   - localhost, 127.0.0.1 and [::1] (if IPv6's loopback works): a certificate for all three from the trusted CA;
//     localhost-as-http is the localhost server at an http URL;
//   - tls12, tls13, rsa, rsa-tls12, p384, hello-retry, asks-for-client-cert: such servers, whose certificates verify;
//   - untrusted, untrusted-root-sent, untrusted-intermediate-sent, self-signed, expired, not-yet-valid, wrong-host
//     (for other.example), wrong-host-by-ip, untrusted-wrong-host, expired-wrong-host, intermediate-not-sent,
//     intermediate-sent, issuer-not-ca, client-auth-only, common-name-only (no subjectAltName), ip-as-dns-name
//     (127.0.0.1 as a DNS name), only-wildcard (*.localhost, at localhost), expired-untrusted, self-signed-expired,
//     intermediate-expired, common-name-wrong,
//     no-names (neither a subjectAltName nor a common name), ips-and-common-name (no DNS name in the
//     subjectAltName) and other-ip (127.0.0.2, if it listens): certificates to verify or not, and those that
//     shouldn't verify record a request that reaches them;
//   - wildcard, wildcard-two-deep, wildcard-bare (*.pts.localhost at a.pts.localhost, a.b.pts.localhost and
//     pts.localhost), wildcard-tld (*.localhost at a.localhost) and wildcard-partial (a*.pts.localhost at
//     ab.pts.localhost), if those names resolve;
//   - plain and plain-other: plain HTTP servers; plain-as-https answers a ClientHello as a plain HTTP server does;
//   - closes (closes the connection after the ClientHello), holds (holds it), tls10, requires-client-cert.
func startTLSLab(t *testing.T) *tlsLab {
	t.Helper()
	dir := t.TempDir()
	lab := &tlsLab{states: map[string]string{}}
	mux := lab.handlers()
	now := time.Now()
	expired := func(c *x509.Certificate) { c.NotBefore, c.NotAfter = now.Add(-48*time.Hour), now.Add(-24*time.Hour) }
	root := newAuthority(t, "PTS Test Root CA", "p256", nil, nil)
	rsaRoot := newAuthority(t, "PTS Test RSA Root CA", "rsa", nil, nil)
	p384Root := newAuthority(t, "PTS Test P-384 Root CA", "p384", nil, nil)
	other := newAuthority(t, "PTS Untrusted Root CA", "p256", nil, nil)
	intermediate := newAuthority(t, "PTS Test Intermediate CA", "p256", root, nil)
	otherIntermediate := newAuthority(t, "PTS Untrusted Intermediate CA", "p256", other, nil)
	expiredIntermediate := newAuthority(t, "PTS Expired Intermediate CA", "p256", root, expired)
	lab.trusted = writePEM(t, dir, "trusted.pem", root.cert, rsaRoot.cert, p384Root.cert)
	lab.untrusted = writePEM(t, dir, "untrusted.pem", other.cert)

	certFor := func(names ...string) func(*x509.Certificate) { // a certificate for names alone, the first its common name
		return func(c *x509.Certificate) {
			c.Subject.CommonName = names[0]
			c.DNSNames, c.IPAddresses = names, nil
		}
	}
	otherHost := certFor("other.example")
	good := serverCert(t, root, "p256", nil)

	main := lab.serveTLS(t, mux, "", good, nil, "")
	lab.name("localhost", "https://localhost:"+main)
	lab.name("127.0.0.1", "https://127.0.0.1:"+main)
	lab.name("localhost-as-http", "http://localhost:"+main)
	if listener, err := net.Listen("tcp", "[::1]:0"); err == nil {
		_ = listener.Close()
		lab.name("[::1]", "https://[::1]:"+lab.serveTLS(t, mux, "[::1]:0", good, nil, ""))
	} else {
		lab.absent = append(lab.absent, "by IPv6 address ")
	}
	if listener, err := net.Listen("tcp", "127.0.0.2:0"); err == nil { // Linux's loopback is 127/8, macOS's 127.0.0.1 alone
		_ = listener.Close()
		lab.name("other-ip", "https://127.0.0.2:"+lab.serveTLS(t, lab.mustNotReach("other-ip", mux), "127.0.0.2:0", good, nil, ""))
	} else {
		lab.absent = append(lab.absent, "by another IP address ")
	}
	// A certificate whose issuer isn't a CA (basicConstraints CA:FALSE); the server sends both.
	notCAKey := newKey(t, "p256")
	notCA := certify(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: "PTS Not A CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, BasicConstraintsValid: true, IsCA: false,
	}, notCAKey, root)
	usageOf := func(usage ...x509.ExtKeyUsage) func(*x509.Certificate) {
		return func(c *x509.Certificate) { c.ExtKeyUsage = usage }
	}
	clientsIntermediate := newAuthority(t, "PTS Intermediate CA for clients", "p256", root, usageOf(x509.ExtKeyUsageClientAuth))
	emailIntermediate := newAuthority(t, "PTS Intermediate CA for email", "p256", root, usageOf(x509.ExtKeyUsageEmailProtection))
	pathZero := newAuthority(t, "PTS Intermediate CA of path length 0", "p256", root, func(c *x509.Certificate) { c.MaxPathLen, c.MaxPathLenZero = 0, true })
	underPathZero := newAuthority(t, "PTS Intermediate CA under path length 0", "p256", pathZero, nil)
	rsa1024Intermediate := newAuthority(t, "PTS RSA 1024 Intermediate CA", "rsa1024", root, nil)
	noCertSign := newAuthority(t, "PTS Intermediate CA without keyCertSign", "p256", root, func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageDigitalSignature })
	sameNameRoot := newAuthority(t, "PTS Test Root CA", "p256", nil, nil)                  // the trusted root's name, another key
	sameNameIntermediate := newAuthority(t, "PTS Test Intermediate CA", "p256", root, nil) // the intermediate's name, another key
	expiredPathZero := newAuthority(t, "PTS Expired Intermediate CA of path length 0", "p256", root, func(c *x509.Certificate) {
		c.MaxPathLen, c.MaxPathLenZero = 0, true
		expired(c)
	})
	underExpiredPathZero := newAuthority(t, "PTS Intermediate CA under an expired one of path length 0", "p256", expiredPathZero, nil)
	expiredNotCAKey := newKey(t, "p256")
	expiredNotCA := certify(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: "PTS Expired Not A CA"}, NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(-24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, BasicConstraintsValid: true, IsCA: false,
	}, expiredNotCAKey, root)
	servers := []struct {
		name     string
		cert     tls.Certificate
		config   func(*tls.Config)
		verifies bool
	}{
		{"tls12", good, func(c *tls.Config) { c.MaxVersion = tls.VersionTLS12 }, true},
		{"tls13", good, func(c *tls.Config) { c.MinVersion = tls.VersionTLS13 }, true},
		{"rsa", serverCert(t, rsaRoot, "rsa", nil), nil, true},
		{"rsa-tls12", serverCert(t, rsaRoot, "rsa", nil), func(c *tls.Config) { c.MaxVersion = tls.VersionTLS12 }, true},
		{"p384", serverCert(t, p384Root, "p256", nil), nil, true},
		{"hello-retry", good, func(c *tls.Config) {
			c.MinVersion = tls.VersionTLS13
			c.CurvePreferences = []tls.CurveID{tls.CurveP384} // a key share clients don't send first
		}, true},
		{"asks-for-client-cert", good, func(c *tls.Config) { c.ClientAuth = tls.RequestClientCert }, true},
		{"untrusted", serverCert(t, other, "p256", nil), nil, false},
		{"untrusted-root-sent", serverCert(t, other, "p256", nil, other.cert), nil, false},
		{"untrusted-intermediate-sent", serverCert(t, otherIntermediate, "p256", nil, otherIntermediate.cert), nil, false},
		{"self-signed", serverCert(t, nil, "p256", nil), nil, false},
		{"expired", serverCert(t, root, "p256", expired), nil, false},
		{"not-yet-valid", serverCert(t, root, "p256", func(c *x509.Certificate) { c.NotBefore, c.NotAfter = now.Add(24*time.Hour), now.Add(48*time.Hour) }), nil, false},
		{"wrong-host", serverCert(t, root, "p256", otherHost), nil, false},
		{"untrusted-wrong-host", serverCert(t, other, "p256", otherHost), nil, false},
		{"expired-wrong-host", serverCert(t, root, "p256", func(c *x509.Certificate) { otherHost(c); expired(c) }), nil, false},
		{"intermediate-not-sent", serverCert(t, intermediate, "p256", nil), nil, false},
		{"intermediate-sent", serverCert(t, intermediate, "p256", nil, intermediate.cert), nil, true},
		{"issuer-not-ca", serverCert(t, &authority{notCA, notCAKey}, "p256", nil, notCA), nil, false},
		{"client-auth-only", serverCert(t, root, "p256", func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} }), nil, false},
		{"common-name-only", serverCert(t, root, "p256", func(c *x509.Certificate) { c.DNSNames, c.IPAddresses = nil, nil }), nil, true},
		{"only-wildcard", serverCert(t, root, "p256", certFor("*.localhost")), nil, false},
		{"expired-untrusted", serverCert(t, other, "p256", expired), nil, false},
		{"self-signed-expired", serverCert(t, nil, "p256", expired), nil, false},
		{"intermediate-expired", serverCert(t, expiredIntermediate, "p256", nil, expiredIntermediate.cert), nil, false},
		{"common-name-wrong", serverCert(t, root, "p256", func(c *x509.Certificate) {
			c.Subject.CommonName = "other.example"
			c.DNSNames, c.IPAddresses = nil, nil
		}), nil, false},
		{"no-names", serverCert(t, root, "p256", func(c *x509.Certificate) {
			c.Subject = pkix.Name{Organization: []string{"ParallelScript tests"}}
			c.DNSNames, c.IPAddresses = nil, nil
		}), nil, false},
		{"ips-and-common-name", serverCert(t, root, "p256", func(c *x509.Certificate) { c.DNSNames = nil }), nil, true},
		{"intermediate-for-clients", serverCert(t, clientsIntermediate, "p256", nil, clientsIntermediate.cert), nil, false},
		{"intermediate-for-email", serverCert(t, emailIntermediate, "p256", nil, emailIntermediate.cert), nil, false},
		{"any-purpose-only", serverCert(t, root, "p256", usageOf(x509.ExtKeyUsageAny)), nil, false},
		{"untrusted-client-auth-only", serverCert(t, other, "p256", usageOf(x509.ExtKeyUsageClientAuth)), nil, false},
		{"broken-signature-intermediate", broken(serverCert(t, intermediate, "p256", nil, intermediate.cert)), nil, false},
		{"broken-signature-root", broken(serverCert(t, root, "p256", nil)), nil, false},
		{"path-length-exceeded", serverCert(t, underPathZero, "p256", nil, underPathZero.cert, pathZero.cert), nil, false},
		{"rsa1024", serverCert(t, rsaRoot, "rsa1024", nil), nil, false},
		{"rsa1024-intermediate", serverCert(t, rsa1024Intermediate, "p256", nil, rsa1024Intermediate.cert), nil, false},
		{"sha1", serverCert(t, rsaRoot, "rsa", func(c *x509.Certificate) { c.SignatureAlgorithm = x509.SHA1WithRSA }), nil, false},
		{"no-cert-sign", serverCert(t, noCertSign, "p256", nil, noCertSign.cert), nil, false},
		{"bmp-common-name", serverCert(t, root, "p256", func(c *x509.Certificate) {
			c.RawSubject = commonNameSubject(t, asn1.TagBMPString, bmpString("localhost"))
			c.DNSNames, c.IPAddresses = nil, nil
		}), nil, true},
		{"bmp-common-name-odd-length", serverCert(t, root, "p256", func(c *x509.Certificate) {
			c.RawSubject = commonNameSubject(t, asn1.TagBMPString, append(bmpString("localhost"), 'A'))
			c.DNSNames, c.IPAddresses = nil, nil
		}), nil, false},
		{"bmp-common-name-surrogate", serverCert(t, root, "p256", func(c *x509.Certificate) {
			c.RawSubject = commonNameSubject(t, asn1.TagBMPString, append([]byte{0xd8, 0x3d, 0xde, 0x00}, bmpString("localhost")...))
			c.DNSNames, c.IPAddresses = nil, nil
		}), nil, false},
		{"same-name-root", serverCert(t, sameNameRoot, "p256", nil), nil, false},
		{"same-name-intermediate", serverCert(t, sameNameIntermediate, "p256", nil, intermediate.cert), nil, false},
		{"path-length-exceeded-expired", serverCert(t, underExpiredPathZero, "p256", nil, underExpiredPathZero.cert, expiredPathZero.cert), nil, false},
		{"issuer-not-ca-expired", serverCert(t, &authority{expiredNotCA, expiredNotCAKey}, "p256", nil, expiredNotCA), nil, false},
		{"comma-name", serverCert(t, root, "p256", certFor("localhost, DNS:x")), nil, false},
	}
	// CAs with name constraints, which no client of this lab may trust: mbedTLS can't check them (D-223).
	constrained := func(name string, edit func(*x509.Certificate)) tls.Certificate {
		ca := newAuthority(t, name, "p256", root, edit)
		return serverCert(t, ca, "p256", certFor("localhost"), ca.cert)
	}
	for _, server := range []struct {
		name string
		cert tls.Certificate
	}{
		{"constrained-elsewhere", constrained("PTS CA for example.org", func(c *x509.Certificate) { c.PermittedDNSDomains = []string{"example.org"} })},
		{"constrained-excluded", constrained("PTS CA except localhost", func(c *x509.Certificate) { c.ExcludedDNSDomains = []string{"localhost"} })},
		{"constrained-permitted", constrained("PTS CA for localhost", func(c *x509.Certificate) { c.PermittedDNSDomains = []string{"localhost"} })},
		{"constrained-critical", constrained("PTS CA for localhost, critically", func(c *x509.Certificate) {
			c.PermittedDNSDomains, c.PermittedDNSDomainsCritical = []string{"localhost"}, true
		})},
	} {
		lab.name(server.name, "https://localhost:"+lab.serveTLS(t, lab.mustNotReach(server.name, mux), "", server.cert, nil, ""))
	}
	for _, server := range servers {
		handler := http.Handler(mux)
		if !server.verifies {
			handler = lab.mustNotReach(server.name, mux)
		}
		lab.name(server.name, "https://localhost:"+lab.serveTLS(t, handler, "", server.cert, server.config, ""))
	}
	lab.name("wrong-host-by-ip", strings.Replace(lab.base("wrong-host"), "localhost", "127.0.0.1", 1))
	lab.name("ip-as-dns-name", "https://127.0.0.1:"+lab.serveTLS(t, lab.mustNotReach("ip-as-dns-name", mux), "", serverCert(t, root, "p256", certFor("127.0.0.1")), nil, ""))
	lab.name("tls10", "https://localhost:"+lab.serveTLS(t, mux, "", good, func(c *tls.Config) { c.MinVersion, c.MaxVersion = tls.VersionTLS10, tls.VersionTLS10 }, "/tls10"))
	lab.name("requires-client-cert", "https://localhost:"+lab.serveTLS(t, mux, "", good, func(c *tls.Config) { c.ClientAuth = tls.RequireAnyClientCert }, "/requires-client-cert"))
	if !resolvesToLoopback(wildcardNames...) {
		lab.absent = append(lab.absent, "wildcard")
	} else {
		wildcard := serverCert(t, root, "p256", certFor("*.pts.localhost"))
		lab.name("wildcard", "https://a.pts.localhost:"+lab.serveTLS(t, mux, "", wildcard, nil, ""))
		mismatched := lab.serveTLS(t, lab.mustNotReach("wildcard-mismatched", mux), "", wildcard, nil, "") // the same certificate
		lab.name("wildcard-two-deep", "https://a.b.pts.localhost:"+mismatched)
		lab.name("wildcard-bare", "https://pts.localhost:"+mismatched)
		lab.name("wildcard-tld", "https://a.localhost:"+strings.TrimPrefix(lab.base("only-wildcard"), "https://localhost:"))
		lab.name("wildcard-partial", "https://ab.pts.localhost:"+lab.serveTLS(t, mux, "", serverCert(t, root, "p256", certFor("a*.pts.localhost")), nil, ""))
	}

	plain := listen(t, "127.0.0.1:0")
	serve(t, plain, mux)
	lab.name("plain", "http://127.0.0.1:"+port(plain))
	plainOther := listen(t, "127.0.0.1:0")
	serve(t, plainOther, mux)
	lab.name("plain-other", "http://127.0.0.1:"+port(plainOther))
	lab.name("plain-as-https", "https://127.0.0.1:"+lab.serveRaw(t, "/plain-as-https", answerAfterHello))
	lab.name("closes", "https://localhost:"+lab.serveRaw(t, "/closes", closeAfterHello))
	lab.name("holds", "https://localhost:"+lab.serveRaw(t, "/holds", holdAfterHello))
	return lab
}

// ---------------------------------------------------------------- running the programs

// tlsProgramTimeout is how long a TLS program may run: longer than other programs, for
// the thousand handshakes of fetch_tls_parallel.pts built with sanitizers on a busy host.
const tlsProgramTimeout = 3 * programTimeout

// trustVars are the environment variables that choose which certificates a client trusts.
var trustVars = []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "NODE_OPTIONS", "NODE_USE_SYSTEM_CA"}

// runTLSProgram runs name with args in an environment of the test's own, without the
// host's trustVars, with env's "NAME=value" settings added.
func runTLSProgram(t *testing.T, env []string, name string, args ...string) runResult {
	t.Helper()
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	ctx, cancel := context.WithTimeout(t.Context(), tlsProgramTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		if !slices.Contains(trustVars, key) {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(append(cmd.Env, "TZ="+testTimeZone), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := runResult{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		result.exitCode = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("running %s: %v", name, err)
	}
	return result
}

// buildShared builds http/<name>.pts once for every test that runs it (as runPts builds
// run tests), and returns the executable's path.
func buildShared(t *testing.T, name string) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("http", name+".pts"))
	assert.NilError(t, err)
	entry, _ := builds.LoadOrStore(source, &programBuild{})
	build := entry.(*programBuild)
	build.once.Do(func() {
		heavyJobs <- struct{}{}
		defer func() { <-heavyJobs }()
		build.exe = filepath.Join(buildDir, "http_"+name+".pts")
		build.err = driver.Build(context.Background(), source, build.exe)
	})
	assert.NilError(t, build.err)
	return build.exe
}

// nodeProgram writes http/<name>.pts as an ES module for Node (runNode's way) and returns the arguments that run it.
func nodeProgram(t *testing.T, name string) []string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("http", name+".pts"))
	assert.NilError(t, err)
	file := filepath.Join(t.TempDir(), name+".mts")
	assert.NilError(t, os.WriteFile(file, []byte(nodeModuleSource(string(source))), 0o644))
	return []string{"--no-warnings", "--experimental-transform-types", file}
}

// nodeIfAny is the path of node, or "" if it isn't on PATH and PTS_REQUIRE_NODE isn't set.
func nodeIfAny(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		assert.Assert(t, os.Getenv(requireNodeVar) == "", "node is not on PATH and %s is set", requireNodeVar)
		t.Log("node is not on PATH: only the recorded Node output is compared")
		return ""
	}
	return node
}

// fetchTLSOutput is what Node printed for http/fetch_tls.pts when the test was written
// (v24.21.0, undici 7.29.1, OpenSSL 3.5.8), its --use-system-ca advice taken out by the
// program. One line ends in a space, after "list:".
const fetchTLSOutput = `get 200 <localhost>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
by IPv4 address 200 <127.0.0.1>/hello GET sni="" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
by IPv6 address 200 <[::1]>/hello GET sni="" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
TLS 1.2 only 200 <tls12>/hello GET sni="localhost" alpn="http/1.1" TLS 1.2, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
TLS 1.3 only 200 <tls13>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
RSA 200 <rsa>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
RSA, TLS 1.2 200 <rsa-tls12>/hello GET sni="localhost" alpn="http/1.1" TLS 1.2, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
P-384 CA 200 <p384>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
hello retry 200 <hello-retry>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
client certificate asked for 200 <asks-for-client-cert>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
post 200 <localhost>/echo POST tls=true length=8 type="text/x-test" authorization="" cookie="" x-kept="" body="body ✓"
chunked 200 <localhost>/chunked one two three
20 MB 200 20971520 end
until close 200 <localhost>/close read until the end
until close without close_notify 200 <localhost>/close-abruptly read until the end
truncated TypeError | other side closed
truncated without close_notify TypeError | other side closed
reset during the body TypeError | read ECONNRESET
http to https 200 redirected <localhost>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
https to http 200 redirected <plain>/hello GET plain
https to https 200 redirected <intermediate-sent>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
307 post, http to https 200 redirected <localhost>/echo POST tls=true length=4 type="text/plain;charset=UTF-8" authorization="" cookie="" x-kept="" body="kept"
303 post, https to http 200 redirected <plain>/echo GET tls=false length=0 type="" authorization="" cookie="" x-kept="" body=""
credentials, https to https on the same origin 200 redirected <localhost>/echo GET tls=true length=0 type="" authorization="Bearer secret" cookie="a=1" x-kept="yes" body=""
credentials, https to http 200 redirected <plain>/echo GET tls=false length=0 type="" authorization="" cookie="" x-kept="yes" body=""
credentials, http to http on another port 200 redirected <plain-other>/echo GET tls=false length=0 type="" authorization="" cookie="" x-kept="yes" body=""
https to an untrusted server TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate
untrusted CA TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate
untrusted CA, root sent TypeError: fetch failed | SELF_SIGNED_CERT_IN_CHAIN | self-signed certificate in certificate chain
untrusted CA, intermediate sent TypeError: fetch failed | UNABLE_TO_GET_ISSUER_CERT_LOCALLY | unable to get local issuer certificate
self-signed TypeError: fetch failed | DEPTH_ZERO_SELF_SIGNED_CERT | self-signed certificate
expired TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
not yet valid TypeError: fetch failed | CERT_NOT_YET_VALID | certificate is not yet valid
wrong host TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: localhost. is not in the cert's altnames: DNS:other.example
wrong host, by IP address TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: IP: 127.0.0.1 is not in the cert's list: 
untrusted CA and wrong host TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate
expired and wrong host TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
expired and untrusted CA TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
expired and self-signed TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
intermediate expired TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
intermediate not sent TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate
intermediate sent 200 <intermediate-sent>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
issuer not a CA TypeError: fetch failed | INVALID_PURPOSE | unsuitable certificate purpose
for clients only TypeError: fetch failed | INVALID_PURPOSE | unsuitable certificate purpose
common name only 200 <common-name-only>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
common name only, wrong TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: localhost. is not cert's CN: other.example
no names TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Cert does not contain a DNS name
IP addresses and a common name 200 <ips-and-common-name>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
IP address as a DNS name TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: IP: 127.0.0.1 is not in the cert's list: 
by another IP address TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: IP: 127.0.0.2 is not in the cert's list: 127.0.0.1, ::1
only a wildcard, at localhost TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: localhost. is not in the cert's altnames: DNS:*.localhost
wildcard 200 <wildcard>/hello GET sni="a.pts.localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
wildcard, two labels deep TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: a.b.pts.localhost. is not in the cert's altnames: DNS:*.pts.localhost
wildcard, the bare domain TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: pts.localhost. is not in the cert's altnames: DNS:*.pts.localhost
wildcard of a top-level domain TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: a.localhost. is not in the cert's altnames: DNS:*.localhost
wildcard, partial 200 <wildcard-partial>/hello GET sni="ab.pts.localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
intermediate for clients only TypeError: fetch failed | INVALID_PURPOSE | unsuitable certificate purpose
intermediate for email only TypeError: fetch failed | INVALID_PURPOSE | unsuitable certificate purpose
for any purpose only TypeError: fetch failed | INVALID_PURPOSE | unsuitable certificate purpose
untrusted CA, for clients only TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate
signature broken, intermediate sent TypeError: fetch failed | CERT_SIGNATURE_FAILURE | certificate signature failure
signature broken, by the root TypeError: fetch failed | CERT_SIGNATURE_FAILURE | certificate signature failure
path length exceeded TypeError: fetch failed | PATH_LENGTH_EXCEEDED | path length constraint exceeded
RSA 1024 key TypeError: fetch failed | UNSPECIFIED | EE certificate key too weak
RSA 1024 intermediate TypeError: fetch failed | UNSPECIFIED | CA certificate key too weak
SHA-1 signature TypeError: fetch failed | UNSPECIFIED | CA signature digest algorithm too weak
intermediate without keyCertSign TypeError: fetch failed | UNSPECIFIED | key usage does not include certificate signing
common name as a BMPString 200 <bmp-common-name>/hello GET sni="localhost" alpn="http/1.1" TLS 1.3, offered alpn=["http/1.1"] versions=["TLS 1.3" "TLS 1.2"]
a comma in a DNS name TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: Host: localhost. is not in the cert's altnames: DNS:"localhost\u002c DNS:x"
a CA named as the trusted root TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate
a CA named as the intermediate sent TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate
path length exceeded by an expired CA TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
expired issuer not a CA TypeError: fetch failed | CERT_HAS_EXPIRED | certificate has expired
common name as a BMPString of odd length TypeError: fetch failed | the cause is an Error: true
common name as a BMPString with a surrogate TypeError: fetch failed | the cause is an Error: true
requests that reached servers whose certificates don't verify: none
plain HTTP server at https TypeError: fetch failed | the cause is an Error: true
https server at http 400 <localhost-as-http>/hello Client sent an HTTP request to an HTTPS server.

closed during the handshake TypeError: fetch failed | the cause is an Error: true
TLS 1.0 only TypeError: fetch failed | the cause is an Error: true
client certificate required TypeError: fetch failed | the cause is an Error: true
their servers read: hello, hello, hello, hello
abort during the handshake held: true, AbortError
abort during the body stalled: true, AbortError, the connection closed: true
`

// expectedTLSOutput is fetchTLSOutput without the lines of servers this lab lacks: by
// IPv6 address where [::1] doesn't listen, by another IP address where 127.0.0.2 doesn't,
// the wildcard ones where their names don't resolve.
func (lab *tlsLab) expectedTLSOutput() string {
	var kept []string
	for _, line := range strings.SplitAfter(fetchTLSOutput, "\n") {
		if !slices.ContainsFunc(lab.absent, func(label string) bool { return strings.HasPrefix(line, label) }) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "")
}

// TestFetchHTTPSMatchesNode runs http/fetch_tls.pts (H33) against the lab's servers under
// pts and under Node, both trusting the lab's CAs through NODE_EXTRA_CA_CERTS: each must
// print what Node printed when the test was written (fetchTLSOutput), so that the test
// isn't vacuous without Node. It covers what the client offers and gets (SNI only for a
// DNS name, ALPN http/1.1, TLS 1.3 or 1.2), RSA, P-384 and hello-retry handshakes, bodies
// of each framing, redirects across schemes and what they drop, Node's code and message
// for each certificate that doesn't verify (and that no request reaches its server),
// failures of the protocol, and aborts during the handshake and the body (whose
// connection closes).
func TestFetchHTTPSMatchesNode(t *testing.T) {
	t.Parallel()
	node := nodeIfAny(t)
	exe := buildShared(t, "fetch_tls")
	lab := startTLSLab(t)
	env := []string{"NODE_EXTRA_CA_CERTS=" + lab.trusted}
	want := lab.expectedTLSOutput()
	if node != "" {
		lab.reset()
		got := runTLSProgram(t, env, node, append(nodeProgram(t, "fetch_tls"), lab.args...)...)
		assert.Equal(t, got.stdout, want, "Node prints other than it did; stderr:\n%s", got.stderr)
		assert.Equal(t, got.exitCode, 0, "stderr:\n%s", got.stderr)
	}
	lab.reset()
	got := runTLSProgram(t, env, exe, lab.args...)
	assert.Equal(t, got.stdout, want, "stderr:\n%s", got.stderr)
	assert.Equal(t, got.exitCode, 0, "stderr:\n%s", got.stderr)
}

// fetchTLSParallelOutput is what http/fetch_tls_parallel.pts prints.
const fetchTLSParallelOutput = `aborted during the handshake: true AbortError the connection closed: true
on one loop: ok 32
from .par.map: ok 32
from parallel tasks: ok 32
aborted at random stages from parallel tasks: ok 64
requests: 1000
files as before: true
grew less than 40 MB: true
`

// TestFetchHTTPSEveryLoop runs http/fetch_tls_parallel.pts (H33) on one loop and on four:
// fetches over TLS on one loop at once, from `.par.map` callbacks and from `parallel`
// tasks, to a server whose certificate verifies and to one whose doesn't, each checked;
// an abort during the handshake, which closes the connection; fetches aborted at random
// stages of their handshakes; and a thousand more, after which the process has the files it
// had and hasn't grown by more than 40 kB a connection.
func TestFetchHTTPSEveryLoop(t *testing.T) {
	t.Parallel()
	exe := buildShared(t, "fetch_tls_parallel")
	lab := startTLSLab(t)
	for _, threads := range []string{"1", "4"} {
		lab.reset()
		got := runTLSProgram(t, []string{"NODE_EXTRA_CA_CERTS=" + lab.trusted, "PTS_THREADS=" + threads}, exe, lab.args...)
		assert.Equal(t, got.stdout, fetchTLSParallelOutput, "PTS_THREADS=%s; stderr:\n%s", threads, got.stderr)
		assert.Equal(t, got.exitCode, 0, "PTS_THREADS=%s; stderr:\n%s", threads, got.stderr)
	}
}

// TestFetchHTTPSUnderSanitizers runs the TLS programs (H33) built with AddressSanitizer
// and UndefinedBehaviorSanitizer on one loop and on four, so that the runtime's misuse of
// a TLS session (freed too early or twice, its buffers used after an abort or read past
// their end) fails, as TestFetchUnderSanitizers does for plain HTTP. mbedTLS and libuv
// are built without the sanitizers (vendoredFlags), so faults inside them go unseen.
func TestFetchHTTPSUnderSanitizers(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"fetch_tls", "fetch_tls_parallel"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source, err := filepath.Abs(filepath.Join("http", name+".pts"))
			assert.NilError(t, err)
			exe := buildSanitized(t, source)
			lab := startTLSLab(t)
			want := fetchTLSParallelOutput
			if name == "fetch_tls" {
				want = lab.expectedTLSOutput()
			}
			for _, threads := range []string{"1", "4"} {
				lab.reset()
				got := runTLSProgram(t, []string{"NODE_EXTRA_CA_CERTS=" + lab.trusted, "PTS_THREADS=" + threads,
					"ASAN_OPTIONS=detect_leaks=0:detect_stack_use_after_return=0"}, exe, lab.args...)
				assert.Equal(t, got.exitCode, 0, "PTS_THREADS=%s:\n%s%s", threads, got.stdout, got.stderr)
				assert.Equal(t, sanitizerNoise.ReplaceAllString(got.stdout, ""), sanitizerNoise.ReplaceAllString(want, ""), "PTS_THREADS=%s; stderr:\n%s", threads, got.stderr)
			}
		})
	}
}

// TestTLSLabIPv6ServerHoldsItsIPv4Port checks that the lab's server on IPv6's loopback
// holds its port on IPv4's too. Where localhost resolves to ::1 first, as on GitHub's
// runners, a request to localhost at a port another server has on 127.0.0.1 (or 0.0.0.0)
// tries [::1] at that port first, and reached a parallel test's lab there: a certificate
// from its root, which has this lab's root's name and another key (CI run 40220044180).
func TestTLSLabIPv6ServerHoldsItsIPv4Port(t *testing.T) {
	t.Parallel()
	lab := startTLSLab(t)
	if slices.Contains(lab.absent, "by IPv6 address ") {
		t.Skip("no IPv6 loopback")
	}
	ipv6Port := strings.TrimPrefix(lab.base("[::1]"), "https://[::1]:")
	listener, err := net.Listen("tcp", "127.0.0.1:"+ipv6Port)
	if err == nil {
		_ = listener.Close()
	}
	assert.Assert(t, err != nil, "127.0.0.1:%s is free while the lab's [::1] server listens at that port", ipv6Port)
}

// TestFetchHTTPSTrustStore runs http/fetch_tls_one.pts (H33) against a server whose
// certificate comes from a throwaway CA, trusted or not as the environment says: the
// system's CAs alone don't trust it; SSL_CERT_FILE replaces the system's bundle, and
// SSL_CERT_DIR (directories, colon-separated, whose every file is read) its directories,
// as OpenSSL and Go; NODE_EXTRA_CA_CERTS adds to whichever, as Node. A file that doesn't
// exist trusts nothing more.
func TestFetchHTTPSTrustStore(t *testing.T) {
	t.Parallel()
	exe := buildShared(t, "fetch_tls_one")
	lab := startTLSLab(t)
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	trustedDir := filepath.Join(dir, "trusted")
	assert.NilError(t, os.Mkdir(empty, 0o755))
	assert.NilError(t, os.Mkdir(trustedDir, 0o755))
	trusted, err := os.ReadFile(lab.trusted)
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(filepath.Join(trustedDir, "test-cas.pem"), trusted, 0o644))
	missing := filepath.Join(dir, "missing.pem")
	const untrusted = "TypeError: fetch failed | UNABLE_TO_VERIFY_LEAF_SIGNATURE | unable to verify the first certificate\n"
	for _, run := range []struct {
		name string
		env  []string
		want string
	}{
		{"the system's CAs", nil, untrusted},
		{"SSL_CERT_FILE trusted", []string{"SSL_CERT_FILE=" + lab.trusted, "SSL_CERT_DIR=" + empty}, "200\n"},
		{"SSL_CERT_FILE untrusted", []string{"SSL_CERT_FILE=" + lab.untrusted, "SSL_CERT_DIR=" + empty}, untrusted},
		{"SSL_CERT_FILE missing", []string{"SSL_CERT_FILE=" + missing, "SSL_CERT_DIR=" + empty}, untrusted},
		{"SSL_CERT_DIR trusted", []string{"SSL_CERT_FILE=" + lab.untrusted, "SSL_CERT_DIR=" + trustedDir}, "200\n"},
		{"SSL_CERT_DIR second of two", []string{"SSL_CERT_FILE=" + lab.untrusted, "SSL_CERT_DIR=" + empty + ":" + trustedDir}, "200\n"},
		{"NODE_EXTRA_CA_CERTS adds", []string{"SSL_CERT_FILE=" + lab.untrusted, "SSL_CERT_DIR=" + empty, "NODE_EXTRA_CA_CERTS=" + lab.trusted}, "200\n"},
		{"NODE_EXTRA_CA_CERTS untrusted", []string{"NODE_EXTRA_CA_CERTS=" + lab.untrusted}, untrusted},
		{"NODE_EXTRA_CA_CERTS missing", []string{"NODE_EXTRA_CA_CERTS=" + missing}, untrusted},
	} {
		got := runTLSProgram(t, run.env, exe, lab.base("localhost")+"/hello")
		assert.Check(t, got.stdout == run.want && got.exitCode == 0, "%s: got exit code %d, stdout %q, want %q; stderr:\n%s", run.name, got.exitCode, got.stdout, run.want, got.stderr)
	}
}

// TestFetchHTTPSNameConstraints runs http/fetch_tls_one.pts (H33) against servers whose
// certificates come from CAs with name constraints, which mbedTLS can't check (D-223):
// a non-critical extension, which mbedTLS ignores, makes the CA untrusted, and a critical
// one fails mbedTLS's parse of the certificate, so no request reaches any of them, where
// Node takes the permitted ones and rejects the others with "permitted subtree violation"
// and "excluded subtree violation".
func TestFetchHTTPSNameConstraints(t *testing.T) {
	t.Parallel()
	exe := buildShared(t, "fetch_tls_one")
	lab := startTLSLab(t)
	const unsupported = "TypeError: fetch failed | UNSPECIFIED | name constraints are not supported: a CA that has them is not trusted\n"
	for _, run := range []struct{ name, want string }{
		{"constrained-elsewhere", unsupported},
		{"constrained-excluded", unsupported},
		{"constrained-permitted", unsupported},
		{"constrained-critical", "TypeError: fetch failed | undefined | X509 - The extension tag or value is invalid : ASN1 - ASN1 tag was of an unexpected value (mbedTLS -0x2562)\n"},
	} {
		got := runTLSProgram(t, []string{"NODE_EXTRA_CA_CERTS=" + lab.trusted}, exe, lab.base(run.name)+"/hello")
		assert.Check(t, got.stdout == run.want && got.exitCode == 0, "%s: got exit code %d, stdout %q, want %q; stderr:\n%s", run.name, got.exitCode, got.stdout, run.want, got.stderr)
	}
	assert.Equal(t, lab.state("/unverified"), "none")
}

// publicSite is the site TestFetchHTTPSPublicSite fetches.
const publicSite = "example.com"

// TestFetchHTTPSPublicSite fetches https://example.com/ (H33) with the system's CAs, also
// when NODE_EXTRA_CA_CERTS adds one and when SSL_CERT_FILE names a file that doesn't exist
// (the system's directories are still read), and requires a 200, as Node gets. It skips
// when the site can't be reached, or Node can't fetch it (a proxy that intercepts TLS).
func TestFetchHTTPSPublicSite(t *testing.T) {
	t.Parallel()
	conn, err := net.DialTimeout("tcp", publicSite+":443", 3*time.Second)
	if err != nil {
		t.Skipf("%s isn't reachable: %v", publicSite, err)
	}
	_ = conn.Close()
	exe := buildShared(t, "fetch_tls_one")
	want := "200\n"
	if node := nodeIfAny(t); node != "" {
		got := runTLSProgram(t, nil, node, append(nodeProgram(t, "fetch_tls_one"), "https://"+publicSite+"/")...)
		if got.stdout != want {
			t.Skipf("Node can't fetch %s either: %s%s", publicSite, got.stdout, got.stderr)
		}
	}
	dir := t.TempDir()
	extra := writePEM(t, dir, "extra.pem", newAuthority(t, "PTS Extra CA", "p256", nil, nil).cert)
	for _, env := range [][]string{nil, {"NODE_EXTRA_CA_CERTS=" + extra}, {"SSL_CERT_FILE=" + filepath.Join(dir, "missing.pem")}} {
		got := runTLSProgram(t, env, exe, "https://"+publicSite+"/")
		assert.Check(t, got.stdout == want && got.exitCode == 0, "%v: got exit code %d, stdout %q, want %q; stderr:\n%s", env, got.exitCode, got.stdout, want, got.stderr)
	}
}
