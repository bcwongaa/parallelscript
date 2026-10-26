package tests

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// ---------------------------------------------------------------- keys and certificates as files

// keyPEM is key as PEM: PKCS#8 ("pkcs8"), PKCS#1 for an RSA key ("pkcs1") or SEC1 for an EC key ("sec1").
func keyPEM(t *testing.T, key crypto.Signer, form string) []byte {
	t.Helper()
	var block *pem.Block
	switch form {
	case "pkcs8":
		der, err := x509.MarshalPKCS8PrivateKey(key)
		assert.NilError(t, err)
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	case "pkcs1":
		block = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key.(*rsa.PrivateKey))}
	case "sec1":
		der, err := x509.MarshalECPrivateKey(key.(*ecdsa.PrivateKey))
		assert.NilError(t, err)
		block = &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}
	default:
		t.Fatalf("no key form %q", form)
	}
	return pem.EncodeToMemory(block)
}

// The object identifiers of an encrypted PKCS#8 key as OpenSSL 3 writes it by default.
var (
	oidPBES2          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
	oidPBKDF2         = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}
	oidHMACWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 9}
	oidAES256CBC      = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
)

// marshalDER is value in DER.
func marshalDER(t *testing.T, value any) []byte {
	t.Helper()
	der, err := asn1.Marshal(value)
	assert.NilError(t, err)
	return der
}

// encryptedPKCS8PEM is key as an encrypted PKCS#8 PEM ("ENCRYPTED PRIVATE KEY"), as
// `openssl pkcs8 -topk8` writes it: PBES2 with PBKDF2 over HMAC-SHA256 and AES-256-CBC.
func encryptedPKCS8PEM(t *testing.T, key crypto.Signer, passphrase string) []byte {
	t.Helper()
	return encryptedPKCS8PEMIterated(t, key, passphrase, 2048)
}

// encryptedPKCS8PEMIterated is encryptedPKCS8PEM with PBKDF2 at the given number of iterations.
func encryptedPKCS8PEMIterated(t *testing.T, key crypto.Signer, passphrase string, iterations int) []byte {
	t.Helper()
	plain, err := x509.MarshalPKCS8PrivateKey(key)
	assert.NilError(t, err)
	salt, iv := make([]byte, 16), make([]byte, aes.BlockSize)
	_, _ = rand.Read(salt)
	_, _ = rand.Read(iv)
	derived, err := pbkdf2.Key(sha256.New, passphrase, salt, iterations, 32)
	assert.NilError(t, err)
	block, err := aes.NewCipher(derived)
	assert.NilError(t, err)
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	sealed := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(sealed, plain)
	kdf := marshalDER(t, struct {
		Salt       []byte
		Iterations int
		PRF        pkix.AlgorithmIdentifier
	}{salt, iterations, pkix.AlgorithmIdentifier{Algorithm: oidHMACWithSHA256, Parameters: asn1.NullRawValue}})
	scheme := marshalDER(t, struct{ KDF, Encryption pkix.AlgorithmIdentifier }{
		pkix.AlgorithmIdentifier{Algorithm: oidPBKDF2, Parameters: asn1.RawValue{FullBytes: kdf}},
		pkix.AlgorithmIdentifier{Algorithm: oidAES256CBC, Parameters: asn1.RawValue{FullBytes: marshalDER(t, iv)}},
	})
	info := marshalDER(t, struct {
		Algorithm pkix.AlgorithmIdentifier
		Data      []byte
	}{pkix.AlgorithmIdentifier{Algorithm: oidPBES2, Parameters: asn1.RawValue{FullBytes: scheme}}, sealed})
	return pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: info})
}

// legacyEncryptedPEM is key in OpenSSL's traditional encrypted PEM (RFC 1423's
// Proc-Type and DEK-Info headers, AES-256-CBC): PKCS#1 for an RSA key, SEC1 for an EC key.
func legacyEncryptedPEM(t *testing.T, key crypto.Signer, passphrase string) []byte {
	t.Helper()
	form := "sec1"
	if _, isRSA := key.(*rsa.PrivateKey); isRSA {
		form = "pkcs1"
	}
	plain, _ := pem.Decode(keyPEM(t, key, form))
	block, err := x509.EncryptPEMBlock(rand.Reader, plain.Type, plain.Bytes, []byte(passphrase), x509.PEMCipherAES256) //nolint:staticcheck // the legacy format is what is tested
	assert.NilError(t, err)
	return pem.EncodeToMemory(block)
}

// chainPEM is the certificates of chain as PEM, in its order: the server's, then those it sends.
func chainPEM(chain tls.Certificate) []byte {
	var out []byte
	for _, der := range chain.Certificate {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return out
}

// signerOf is chain's private key.
func signerOf(chain tls.Certificate) crypto.Signer {
	return chain.PrivateKey.(crypto.Signer)
}

// writeFile writes data to a new file called name in dir and returns its path.
func writeFile(t *testing.T, dir string, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	assert.NilError(t, os.WriteFile(path, data, 0o600))
	return path
}

// pool is a certificate pool of certs.
func pool(certs ...*x509.Certificate) *x509.CertPool {
	roots := x509.NewCertPool()
	for _, cert := range certs {
		roots.AddCert(cert)
	}
	return roots
}

// ---------------------------------------------------------------- clients

// httpsClient is an HTTP/1.1 client that trusts roots alone, edit changing its TLS configuration.
func httpsClient(roots *x509.CertPool, edit func(*tls.Config), keepAlive bool) *http.Client {
	config := &tls.Config{RootCAs: roots}
	if edit != nil {
		edit(config)
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: config, DisableKeepAlives: !keepAlive}, Timeout: 20 * time.Second}
}

// requestWith sends a request with client and returns the response and its body.
func requestWith(t *testing.T, client *http.Client, method string, url string, body string, header ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	assert.NilError(t, err)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	response, err := client.Do(req)
	assert.NilError(t, err)
	defer response.Body.Close()
	text, err := io.ReadAll(response.Body)
	assert.NilError(t, err)
	return response, string(text)
}

// record is the header of a TLS record: its content type and length.
type record struct {
	contentType byte
	length      int
}

// recordingConn is the TCP connection under a TLS client, which keeps the header of each
// TLS record the client reads and whether the client read the TCP connection's end, so
// that a test can see how the server ended the TLS connection.
type recordingConn struct {
	net.Conn
	mu      sync.Mutex
	pending []byte
	records []record
	ended   bool // a read found the TCP connection's end
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ended = c.ended || err == io.EOF
	c.pending = append(c.pending, p[:n]...)
	for len(c.pending) >= 5 {
		length := int(c.pending[3])<<8 | int(c.pending[4])
		if len(c.pending) < 5+length {
			break
		}
		c.records = append(c.records, record{c.pending[0], length})
		c.pending = c.pending[5+length:]
	}
	return n, err
}

// last is the last whole record read, and whether the TCP connection's end was read.
func (c *recordingConn) last() (record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.records) == 0 {
		return record{}, c.ended
	}
	return c.records[len(c.records)-1], c.ended
}

// tlsConn is a TLS connection to a server under test, which writes raw requests and reads responses.
type tlsConn struct {
	*tls.Conn
	tcp    *recordingConn
	reader *bufio.Reader
}

// dialTLS makes a TLS connection to address with config and completes its handshake.
func dialTLS(t *testing.T, address string, config *tls.Config) *tlsConn {
	t.Helper()
	conn, err := handshake(t, address, config)
	assert.NilError(t, err)
	return conn
}

// handshake makes a TLS connection to address with config and returns it once its
// handshake is done, or the handshake's error. The server's name is address's host unless
// config says otherwise (for an IP address, as here, Go sends no SNI).
func handshake(t *testing.T, address string, config *tls.Config) (*tlsConn, error) {
	t.Helper()
	tcp, err := net.DialTimeout("tcp", address, 5*time.Second)
	assert.NilError(t, err)
	if config.ServerName == "" && !config.InsecureSkipVerify {
		config = config.Clone()
		config.ServerName, _, _ = net.SplitHostPort(address)
	}
	recording := &recordingConn{Conn: tcp}
	conn := tls.Client(recording, config)
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return &tlsConn{Conn: conn, tcp: recording, reader: bufio.NewReader(conn)}, nil
}

func (c *tlsConn) send(t *testing.T, raw string) {
	t.Helper()
	_, err := c.Write([]byte(raw))
	assert.NilError(t, err)
}

// response reads the next response and its body.
func (c *tlsConn) response(t *testing.T) (*http.Response, string) {
	t.Helper()
	assert.NilError(t, c.SetReadDeadline(time.Now().Add(20*time.Second)))
	response, err := http.ReadResponse(c.reader, nil)
	assert.NilError(t, err)
	body, err := io.ReadAll(response.Body)
	assert.NilError(t, err)
	return response, string(body)
}

// rest reads what the server sends until it ends the connection, and fails if it doesn't within a few seconds.
func (c *tlsConn) rest(t *testing.T) string {
	t.Helper()
	assert.NilError(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
	got, err := io.ReadAll(c.reader)
	assert.Assert(t, !isTimeout(err), "the server didn't end the connection; read %q", got)
	return string(got)
}

// closedByServer reports whether the server ended the connection, with or without
// close_notify or by a reset, without sending anything more.
func (c *tlsConn) closedByServer(t *testing.T) bool {
	t.Helper()
	assert.NilError(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
	n, err := c.reader.Read(make([]byte, 1))
	return n == 0 && err != nil && !isTimeout(err)
}

// endedWithCloseNotify checks that the server sends nothing more and ends the TLS
// connection with a close_notify alert, then the TCP connection. Go's client takes the TCP
// connection's end at a record's end as it takes close_notify, so the check is that the
// client stopped at a record (close_notify) before the TCP connection's end, which comes
// after it. (Under TLS 1.3 the alert's record looks like any other.)
func (c *tlsConn) endedWithCloseNotify(t *testing.T) {
	t.Helper()
	assert.Equal(t, c.rest(t), "", "the server sent more")
	last, ended := c.tcp.last()
	assert.Assert(t, !ended, "the TCP connection ended without close_notify; the last record was %+v", last)
	if c.ConnectionState().Version == tls.VersionTLS12 {
		assert.Equal(t, last.contentType, byte(21), "the last record isn't an alert: %+v", last)
	}
	assert.NilError(t, c.tcp.SetReadDeadline(time.Now().Add(5*time.Second)))
	n, err := c.tcp.Conn.Read(make([]byte, 1))
	assert.Assert(t, n == 0 && err == io.EOF, "the TCP connection didn't end after close_notify: %d bytes, %v", n, err)
}

// clientHello is the first record of a TLS client's handshake: its ClientHello.
func clientHello(t *testing.T) []byte {
	t.Helper()
	client, server := net.Pipe()
	defer server.Close()
	go func() {
		_ = tls.Client(client, &tls.Config{ServerName: "localhost"}).Handshake()
		_ = client.Close()
	}()
	assert.NilError(t, server.SetReadDeadline(time.Now().Add(5*time.Second)))
	header := make([]byte, 5)
	_, err := io.ReadFull(server, header)
	assert.NilError(t, err)
	body := make([]byte, int(header[3])<<8|int(header[4]))
	_, err = io.ReadFull(server, body)
	assert.NilError(t, err)
	return append(header, body...)
}

// garbageHello is a handshake record holding a ClientHello four bytes long, which no TLS
// server can parse. (Garbage of a handshake message's length still to come would only
// make the server wait for the rest.)
var garbageHello = []byte("\x16\x03\x01\x00\x08\x01\x00\x00\x04\x03\x03\x00\x00")

// noHTTPAnswer checks that the server answers raw, sent to its TLS port over TCP, with no
// HTTP response, at most a TLS alert, and closes the connection.
func noHTTPAnswer(t *testing.T, address string, raw []byte) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	assert.NilError(t, err)
	defer conn.Close()
	_, err = conn.Write(raw)
	assert.NilError(t, err)
	assert.NilError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	got, err := io.ReadAll(conn)
	assert.Assert(t, !isTimeout(err), "the server didn't close the connection; it sent %q", got)
	assert.Assert(t, len(got) == 0 || got[0] == 21, "the server answered %q with %q", raw, got)
}

// numbered is size bytes of lines that each say where they are, so that a body that comes
// back reordered, cut or doubled doesn't compare equal.
func numbered(size int) string {
	var b strings.Builder
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "%015d\n", i)
	}
	return b.String()[:size]
}

// bigBody is what GET /big of server_tls.pts answers: 6 MB of lines of a number after dots.
func bigBody() string {
	var b strings.Builder
	for i := range 384 * 1024 {
		number := fmt.Sprint(i)
		b.WriteString(strings.Repeat(".", 15-len(number)) + number + "\n")
	}
	return b.String()
}

// ---------------------------------------------------------------- the server

// tlsPassphrase is the passphrase of the tests' encrypted keys.
const tlsPassphrase = "correct horse battery staple"

// httpsServer is server_tls.pts running: its servers' addresses by name, and the CA that trusts them.
type httpsServer struct {
	*httpServer
	addresses map[string]string
	roots     *x509.CertPool
	rootFile  string
}

func (s *httpsServer) base(name string) string { return "https://" + s.addresses[name] }

// startHTTPSServer makes the servers' certificates and keys (main's from an intermediate
// CA, which it sends; the others' from the root) and starts exe, server_tls.pts built,
// with them, checking that each server says it is https at the right url.
func startHTTPSServer(t *testing.T, exe string) *httpsServer {
	t.Helper()
	dir := t.TempDir()
	root := newAuthority(t, "PTS Test Root CA", "p256", nil, nil)
	intermediate := newAuthority(t, "PTS Test Intermediate CA", "p256", root, nil)
	main := serverCert(t, intermediate, "p256", nil, intermediate.cert)
	rsaChain := serverCert(t, root, "rsa", nil)
	p384 := serverCert(t, root, "p384", nil)
	encrypted := serverCert(t, root, "p256", nil)
	legacy := serverCert(t, root, "rsa", nil)
	running, fields := startServer(t, exe,
		"MAIN_CERT_FILE="+writeFile(t, dir, "main.pem", chainPEM(main)),
		"MAIN_KEY_FILE="+writeFile(t, dir, "main-key.pem", keyPEM(t, signerOf(main), "pkcs8")),
		"RSA_CERT="+string(chainPEM(rsaChain)),
		"RSA_KEY="+string(keyPEM(t, signerOf(rsaChain), "pkcs1")),
		"P384_CERT_FILE="+writeFile(t, dir, "p384.pem", chainPEM(p384)),
		"P384_KEY="+string(keyPEM(t, signerOf(p384), "sec1")),
		"ENCRYPTED_CERT="+string(chainPEM(encrypted)),
		"ENCRYPTED_KEY_FILE="+writeFile(t, dir, "encrypted-key.pem", encryptedPKCS8PEM(t, signerOf(encrypted), tlsPassphrase)),
		"LEGACY_CERT_FILE="+writeFile(t, dir, "legacy.pem", chainPEM(legacy)),
		"LEGACY_KEY_FILE="+writeFile(t, dir, "legacy-key.pem", legacyEncryptedPEM(t, signerOf(legacy), tlsPassphrase)),
		"PASSPHRASE="+tlsPassphrase)
	server := &httpsServer{httpServer: running, addresses: map[string]string{}, roots: pool(root.cert), rootFile: writePEM(t, dir, "root.pem", root.cert)}
	for _, field := range fields {
		name, port, _ := strings.Cut(field, "=")
		server.addresses[name] = "127.0.0.1:" + port
	}
	for _, name := range []string{"main", "rsa", "p384", "encrypted", "legacy"} {
		assert.Assert(t, server.addresses[name] != "", "no server %s: %v", name, fields)
		assert.Assert(t, server.lines.Scan(), "stderr:\n%s", server.stderr)
		assert.Equal(t, server.lines.Text(), name+" https true", "protocol and url")
	}
	return server
}

// TestHTTPSServer runs pts:http's server over TLS (H33b), each server of server_tls.pts
// with a certificate and key in another form, against Go's client trusting only the
// test's throwaway root CA: the routes, bodies and headers of TestHTTPServer, keep-alive,
// TLS 1.2 and 1.3 with ECDSA (P-256, P-384) and RSA keys, a chain whose intermediate the
// server sends, ALPN, SNI, the raw protocol over TLS (pipelining, HTTP/1.0, chunked
// bodies, 100-continue, the server's 400, 413 and 431, each ending with close_notify),
// bodies of several megabytes, plain HTTP and garbage at the TLS port, clients that stall
// or leave during their handshakes, Node's clients, and stop(), which ends the program
// with nothing on stderr.
func TestHTTPSServer(t *testing.T) {
	t.Parallel()
	checkHTTPSServer(t, startHTTPSServer(t, buildServer(t, "server_tls")))
}

// checkHTTPSServer talks to server_tls.pts running as TestHTTPSServer describes, then stops it.
func checkHTTPSServer(t *testing.T, server *httpsServer) {
	main := server.base("main")
	port := strings.TrimPrefix(server.addresses["main"], "127.0.0.1:")

	t.Run("routes", func(t *testing.T) {
		client := httpsClient(server.roots, nil, true)
		defer client.CloseIdleConnections()
		response, body := requestWith(t, client, "GET", main+"/hello", "")
		assert.Equal(t, response.StatusCode, 200)
		assert.Equal(t, body, "hello")
		assert.Equal(t, response.Header.Get("Content-Type"), "text/plain;charset=UTF-8")
		assert.Equal(t, len(response.TLS.PeerCertificates), 2, "the server sends its certificate and the intermediate")
		assert.Equal(t, len(response.TLS.VerifiedChains[0]), 3, "the chain reaches the root through the intermediate")

		response, body = requestWith(t, client, "POST", main+"/echo", "ünïcode body", "Content-Type", "text/x-test")
		assert.Equal(t, body, "ünïcode body")
		assert.Equal(t, response.Header.Get("Content-Type"), "text/x-test")
		response, body = requestWith(t, client, "GET", main+"/status", "")
		assert.Equal(t, response.Status, "201 Made")
		assert.Equal(t, response.Header.Get("X-Test"), "a, b")
		assert.DeepEqual(t, response.Header.Values("Set-Cookie"), []string{"one=1", "two=2"})
		assert.Equal(t, body, "")
		response, _ = requestWith(t, client, "HEAD", main+"/hello", "")
		assert.Equal(t, response.ContentLength, int64(5))
		date, err := http.ParseTime(response.Header.Get("Date"))
		assert.NilError(t, err)
		assert.Assert(t, time.Since(date).Abs() < time.Minute, date)
		_, body = requestWith(t, client, "GET", main+"/later", "")
		assert.Equal(t, body, "later")
		response, body = requestWith(t, client, "GET", main+"/missing", "")
		assert.Equal(t, response.StatusCode, 404)
		assert.Equal(t, body, "no such page")
		_, body = requestWith(t, client, "GET", main+"/url?q=1", "")
		assert.Equal(t, body, main+"/url?q=1", "request.url is https://, from the Host header")
		_, body = requestWith(t, client, "GET", main+"/server", "")
		assert.Equal(t, body, "https "+main+"/", "the handler's server")
	})

	t.Run("keys and certificates in each form", func(t *testing.T) {
		client := httpsClient(server.roots, nil, false)
		for _, run := range []struct {
			name, key string
		}{{"main", "ECDSA P-256"}, {"rsa", "RSA"}, {"p384", "ECDSA P-384"}, {"encrypted", "ECDSA P-256"}, {"legacy", "RSA"}} {
			for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
				client := httpsClient(server.roots, func(c *tls.Config) { c.MinVersion, c.MaxVersion = version, version }, false)
				response, body := requestWith(t, client, "GET", server.base(run.name)+"/hello", "")
				assert.Equal(t, body, "hello", "%s, %s", run.name, tls.VersionName(version))
				assert.Equal(t, response.TLS.Version, version, run.name)
				assert.Equal(t, keyName(response.TLS.PeerCertificates[0]), run.key, run.name)
			}
		}
		response, _ := requestWith(t, client, "GET", main+"/hello", "")
		assert.Equal(t, response.TLS.Version, uint16(tls.VersionTLS13), "TLS 1.3 when the client offers it")
	})

	t.Run("keep-alive", func(t *testing.T) {
		client := httpsClient(server.roots, nil, true)
		defer client.CloseIdleConnections()
		var reused []bool
		for i := range 4 {
			trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = append(reused, info.Reused) }}
			req, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), "POST", main+"/echo", strings.NewReader(fmt.Sprint(i)))
			assert.NilError(t, err)
			response, err := client.Do(req)
			assert.NilError(t, err)
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			assert.NilError(t, err)
			assert.Equal(t, string(body), fmt.Sprint(i))
		}
		assert.DeepEqual(t, reused, []bool{false, true, true, true})
	})

	t.Run("what the handshake offers and refuses", func(t *testing.T) {
		_, err := handshake(t, server.addresses["main"], &tls.Config{RootCAs: server.roots, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11})
		assert.ErrorContains(t, err, "protocol version", "TLS 1.1 and older are refused")
		for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
			asked := false
			cache := tls.NewLRUClientSessionCache(4)
			for range 2 {
				conn := dialTLS(t, server.addresses["main"], &tls.Config{RootCAs: server.roots, MinVersion: version, MaxVersion: version, ClientSessionCache: cache,
					GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
						asked = true
						return &tls.Certificate{}, nil
					}})
				conn.send(t, "GET /hello HTTP/1.1\r\nHost: a\r\nConnection: close\r\n\r\n")
				_, body := conn.response(t)
				assert.Equal(t, body, "hello")
				assert.Assert(t, !conn.ConnectionState().DidResume, "%s: a session was resumed", tls.VersionName(version))
				conn.endedWithCloseNotify(t)
			}
			assert.Assert(t, !asked, "%s: the server asked for a client certificate", tls.VersionName(version))
		}
	})

	t.Run("ALPN", func(t *testing.T) {
		for _, run := range []struct {
			offered []string
			want    string
		}{{[]string{"h2", "http/1.1"}, "http/1.1"}, {[]string{"http/1.1"}, "http/1.1"}, {nil, ""}} {
			conn := dialTLS(t, server.addresses["main"], &tls.Config{RootCAs: server.roots, NextProtos: run.offered})
			assert.Equal(t, conn.ConnectionState().NegotiatedProtocol, run.want, "offered %q", run.offered)
			conn.send(t, "GET /hello HTTP/1.1\r\nHost: a\r\n\r\n")
			_, body := conn.response(t)
			assert.Equal(t, body, "hello")
		}
		_, err := handshake(t, server.addresses["main"], &tls.Config{RootCAs: server.roots, NextProtos: []string{"h2"}})
		assert.ErrorContains(t, err, "no application protocol", "RFC 7301: a client that offers only protocols the server lacks gets no_application_protocol")
		client := httpsClient(server.roots, nil, false)
		client.Transport.(*http.Transport).ForceAttemptHTTP2 = true // Go's default client offers h2 first
		response, body := requestWith(t, client, "GET", main+"/hello", "")
		assert.Equal(t, response.Proto, "HTTP/1.1")
		assert.Equal(t, body, "hello")
	})

	t.Run("SNI of any name gets the one certificate", func(t *testing.T) {
		for _, name := range []string{"localhost", "anything.example", ""} {
			conn := dialTLS(t, server.addresses["main"], &tls.Config{ServerName: name, InsecureSkipVerify: true,
				VerifyConnection: func(state tls.ConnectionState) error { // verified, as for localhost
					_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: "localhost", Roots: server.roots, Intermediates: pool(state.PeerCertificates[1:]...)})
					return err
				}})
			conn.send(t, "GET /hello HTTP/1.1\r\nHost: a\r\nConnection: close\r\n\r\n")
			_, body := conn.response(t)
			assert.Equal(t, body, "hello", "SNI %q", name)
		}
	})

	t.Run("the protocol inside TLS", func(t *testing.T) {
		config := func(version uint16) *tls.Config {
			return &tls.Config{RootCAs: server.roots, MinVersion: version, MaxVersion: version}
		}
		for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
			address, name := server.addresses["main"], tls.VersionName(version)
			pipelined := dialTLS(t, address, config(version))
			pipelined.send(t, "GET /hello HTTP/1.1\r\nHost: a\r\n\r\nPOST /echo HTTP/1.1\r\nHost: a\r\nContent-Length: 3\r\n\r\nabcGET /url HTTP/1.1\r\nHost: example.test\r\nConnection: close\r\n\r\n")
			for _, want := range []string{"hello", "abc", "https://example.test/url"} {
				_, body := pipelined.response(t)
				assert.Equal(t, body, want, "%s: pipelined responses in order", name)
			}
			pipelined.endedWithCloseNotify(t)

			old := dialTLS(t, address, config(version))
			old.send(t, "GET /url HTTP/1.0\r\n\r\n")
			response, body := old.response(t)
			assert.Assert(t, response.Close, "%s: HTTP/1.0 closes", name)
			assert.Equal(t, body, main+"/url", "%s: without a Host header, request.url is the server's", name)
			old.endedWithCloseNotify(t)

			kept := dialTLS(t, address, config(version))
			kept.send(t, "GET /hello HTTP/1.0\r\nConnection: keep-alive\r\n\r\n")
			response, body = kept.response(t)
			assert.Equal(t, response.Header.Get("Connection"), "keep-alive", name)
			assert.Equal(t, body, "hello")
			kept.send(t, "POST /echo HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n4\r\nchun\r\n3;ext=1\r\nked\r\n0\r\n\r\n")
			_, body = kept.response(t)
			assert.Equal(t, body, "chunked", "%s: a chunked body", name)
			kept.endedWithCloseNotify(t)

			split := dialTLS(t, address, config(version))
			for _, b := range []byte("POST /echo HTTP/1.1\r\nHost: a\r\nContent-Length: 5\r\nConnection: close\r\n\r\nbytes") {
				split.send(t, string(b)) // a record of its own each
			}
			_, body = split.response(t)
			assert.Equal(t, body, "bytes", "%s: a request in a record a byte", name)
			split.endedWithCloseNotify(t)

			continued := dialTLS(t, address, config(version))
			continued.send(t, "POST /echo HTTP/1.1\r\nHost: a\r\nExpect: 100-continue\r\nContent-Length: 4\r\nConnection: close\r\n\r\n")
			assert.NilError(t, continued.SetReadDeadline(time.Now().Add(20*time.Second)))
			interim := make([]byte, len("HTTP/1.1 100 Continue\r\n\r\n"))
			_, err := io.ReadFull(continued.reader, interim)
			assert.NilError(t, err)
			assert.Equal(t, string(interim), "HTTP/1.1 100 Continue\r\n\r\n", name)
			continued.send(t, "body")
			_, body = continued.response(t)
			assert.Equal(t, body, "body", "%s: the body sent after 100 Continue", name)

			for _, run := range []struct{ raw, status string }{
				{"NOT HTTP\r\n\r\n", "400 Bad Request"},
				{"GET /hello HTTP/1.1\r\nHost: a\r\nX-Split: a\rb\r\n\r\n", "400 Bad Request"},
				{"POST /echo HTTP/1.1\r\nHost: a\r\nContent-Length: 100000000\r\n\r\n", "413 Content Too Large"},
				{oversizedHead(), "431 Request Header Fields Too Large"},
			} {
				refused := dialTLS(t, address, config(version))
				refused.send(t, run.raw)
				got := refused.rest(t)
				assert.Assert(t, dated.MatchString(got), got)
				assert.Equal(t, dated.ReplaceAllString(got, ""), "HTTP/1.1 "+run.status+"\r\ncontent-length: 0\r\nconnection: close\r\n\r\n", name)
				refused.endedWithCloseNotify(t)
			}
		}
	})

	t.Run("bodies of megabytes", func(t *testing.T) {
		client := httpsClient(server.roots, nil, true)
		defer client.CloseIdleConnections()
		sent := numbered(5 * 1024 * 1024)
		_, body := requestWith(t, client, "POST", main+"/echo", sent)
		assert.Assert(t, body == sent, "5 MB posted and echoed: %d bytes came back", len(body))
		_, body = requestWith(t, client, "GET", main+"/big", "")
		assert.Assert(t, body == bigBody(), "6 MB answered: %d bytes came", len(body))
		_, body = requestWith(t, client, "GET", main+"/hello", "")
		assert.Equal(t, body, "hello", "the connection goes on after them")
	})

	t.Run("plain HTTP and garbage at the TLS port", func(t *testing.T) {
		noHTTPAnswer(t, server.addresses["main"], []byte("GET /hello HTTP/1.1\r\nHost: a\r\n\r\n"))
		noHTTPAnswer(t, server.addresses["main"], garbageHello)
		noHTTPAnswer(t, server.addresses["main"], bytes.Repeat([]byte{0xff}, 4096))
		_, body := requestWith(t, httpsClient(server.roots, nil, false), "GET", main+"/hello", "")
		assert.Equal(t, body, "hello")
	})

	t.Run("clients that stall or leave during their handshakes", func(t *testing.T) {
		hello := clientHello(t)
		for i := range 24 {
			conn, err := net.DialTimeout("tcp", server.addresses["main"], 5*time.Second)
			assert.NilError(t, err)
			switch i % 3 {
			case 0: // sends nothing, and stays
			case 1: // sends half a ClientHello, and stays
				_, err = conn.Write(hello[:len(hello)/2])
			case 2: // sends a ClientHello and leaves
				_, err = conn.Write(hello)
				_ = conn.(*net.TCPConn).SetLinger(0)
				_ = conn.Close()
			}
			assert.NilError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
		}
		for range 8 { // rejects the server's certificate, with an alert, and leaves
			_, err := handshake(t, server.addresses["main"], &tls.Config{RootCAs: x509.NewCertPool()})
			assert.ErrorContains(t, err, "certificate signed by unknown authority")
		}
		bodies := getAll(t, httpsClient(server.roots, nil, false), main+"/hello", 8, 4)
		assert.DeepEqual(t, tallyBodies(bodies), map[string]int{"hello": 32})
	})

	t.Run("Node's clients", func(t *testing.T) {
		node := nodeIfAny(t)
		if node == "" {
			t.Skip("node is not on PATH")
		}
		script := writeFile(t, t.TempDir(), "client.mjs", []byte(nodeHTTPSClient))
		got := runTLSProgram(t, []string{"NODE_EXTRA_CA_CERTS=" + server.rootFile}, node, script, main)
		assert.Equal(t, got.exitCode, 0, "stderr:\n%s", got.stderr)
		assert.Equal(t, got.stdout, strings.ReplaceAll(nodeHTTPSClientOutput, "PORT", port), "stderr:\n%s", got.stderr)
	})

	// Connections still in their handshakes when stop() comes are idle ones: they close, so the program ends.
	stalled := []net.Conn{}
	for _, sent := range [][]byte{nil, clientHello(t)[:20]} {
		conn, err := net.DialTimeout("tcp", server.addresses["main"], 5*time.Second)
		assert.NilError(t, err)
		_, err = conn.Write(sent)
		assert.NilError(t, err)
		stalled = append(stalled, conn)
	}
	_, body := requestWith(t, httpsClient(server.roots, nil, false), "GET", main+"/stop", "")
	assert.Equal(t, body, "bye")
	awaitExit(t, server.httpServer)
	for _, conn := range stalled {
		_ = conn.Close()
	}
	assert.Equal(t, server.stderr.String(), "", "nothing is printed for handshakes that failed")
}

// keyName is the kind of cert's public key.
func keyName(cert *x509.Certificate) string {
	switch key := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		return "RSA"
	case *ecdsa.PublicKey:
		return "ECDSA " + key.Curve.Params().Name
	}
	return cert.PublicKeyAlgorithm.String()
}

// oversizedHead is a request head without its end that is one byte longer than the 64 KB
// the server takes, so that the server has read every byte of it when it answers 431 and
// closes (bytes it hadn't read would make the close a reset).
func oversizedHead() string {
	const start = "GET /hello HTTP/1.1\r\nHost: a\r\nX-Long: "
	return start + strings.Repeat("a", 64*1024+1-len(start))
}

// nodeHTTPSClient is a Node program that talks to the server at its argument with fetch,
// with https.get (keeping its connection alive, then over TLS 1.2) and with tls.connect
// offering h2 and http/1.1, trusting the test's CA through NODE_EXTRA_CA_CERTS.
const nodeHTTPSClient = `import https from "node:https";
import tls from "node:tls";

const base = process.argv[2];
const response = await fetch(base + "/hello");
console.log("fetch", response.status, await response.text());
const posted = await fetch(base + "/echo", { method: "POST", body: "from node ✓", headers: { "content-type": "text/x-node" } });
console.log("fetch post", posted.status, posted.headers.get("content-type"), await posted.text());

const kept = new https.Agent({ keepAlive: true, maxSockets: 1 });
function get(path, agent) {
  return new Promise((resolve, reject) => {
    const request = https.get(base + path, { agent }, (res) => {
      const protocol = res.socket.getProtocol();
      let body = "";
      res.setEncoding("utf8");
      res.on("data", (chunk) => (body += chunk));
      res.on("end", () => resolve(res.statusCode + " " + body + " reused=" + request.reusedSocket + " " + protocol));
    });
    request.on("error", reject);
  });
}
console.log("https.get", await get("/hello", kept));
console.log("https.get", await get("/url", kept));
kept.destroy();
console.log("https.get, TLS 1.2", await get("/hello", new https.Agent({ maxVersion: "TLSv1.2" })));

const alpn = await new Promise((resolve, reject) => {
  const socket = tls.connect({ host: "127.0.0.1", port: Number(new URL(base).port), ALPNProtocols: ["h2", "http/1.1"] }, () => {
    resolve(socket.alpnProtocol);
    socket.end();
  });
  socket.on("error", reject);
});
console.log("tls.connect, h2 and http/1.1 offered:", alpn);
`

// nodeHTTPSClientOutput is what nodeHTTPSClient prints, PORT standing for the server's port.
const nodeHTTPSClientOutput = `fetch 200 hello
fetch post 200 text/x-node from node ✓
https.get 200 hello reused=false TLSv1.3
https.get 200 https://127.0.0.1:PORT/url reused=true TLSv1.3
https.get, TLS 1.2 200 hello reused=false TLSv1.2
tls.connect, h2 and http/1.1 offered: http/1.1
`

// ---------------------------------------------------------------- every loop

// serverFiles makes a certificate for localhost, 127.0.0.1 and ::1 from a new root CA and
// writes the certificate, its key and the root to PEM files in dir.
func serverFiles(t *testing.T, dir string) (certFile string, keyFile string, root *authority) {
	t.Helper()
	root = newAuthority(t, "PTS Test Root CA", "p256", nil, nil)
	chain := serverCert(t, root, "p256", nil)
	return writeFile(t, dir, "server.pem", chainPEM(chain)), writeFile(t, dir, "server-key.pem", keyPEM(t, signerOf(chain), "pkcs8")), root
}

// TestHTTPSServerEveryLoop runs an HTTPS server with reusePort (H33b) on four loops and
// on one: many clients making fresh TLS connections at once, whose handshakes run on
// every loop, all get their answers; on four loops, handlers on different loops run at
// the same time; keep-alive clients, timers and tasks work; stop() reaches every loop, so
// the program ends, with nothing on stderr.
func TestHTTPSServerEveryLoop(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "every_loop_tls")
	certFile, keyFile, root := serverFiles(t, t.TempDir())
	roots := pool(root.cert)
	for _, threads := range []int{1, 4} {
		t.Run(fmt.Sprintf("PTS_THREADS=%d", threads), func(t *testing.T) {
			t.Parallel()
			server, fields := startServer(t, exe, fmt.Sprintf("PTS_THREADS=%d", threads), "TLS_CERT_FILE="+certFile, "TLS_KEY_FILE="+keyFile)
			assert.DeepEqual(t, fields[1:], []string{"true"})
			base := "https://127.0.0.1:" + fields[0]
			fresh := httpsClient(roots, nil, false)
			requests := 0
			if threads > 1 {
				meetings := getAll(t, fresh, base+"/meet", 8, 1)
				assert.Assert(t, slices.Contains(meetings, "together"), "no two handlers ran at once: %v", meetings)
				requests += len(meetings)
			}
			hellos := getAll(t, fresh, base+"/", 16, 50)
			assert.DeepEqual(t, tallyBodies(hellos), map[string]int{"hello https": 16 * 50})
			kept := httpsClient(roots, nil, true)
			defer kept.CloseIdleConnections()
			keptHellos := getAll(t, kept, base+"/", 8, 8)
			assert.DeepEqual(t, tallyBodies(keptHellos), map[string]int{"hello https": 64})
			_, later := requestWith(t, kept, "GET", base+"/later", "")
			assert.Equal(t, later, "later")
			_, fib := requestWith(t, kept, "GET", base+"/fib", "")
			assert.Equal(t, fib, "6765")
			requests += len(hellos) + len(keptHellos) + 2
			_, count := requestWith(t, kept, "GET", base+"/count", "")
			assert.Equal(t, count, fmt.Sprint(requests+1))
			_, body := requestWith(t, fresh, "GET", base+"/stop", "")
			assert.Equal(t, body, "bye")
			awaitExit(t, server)
			assert.Equal(t, server.stderr.String(), "")
		})
	}
}

// ---------------------------------------------------------------- the lifecycle

// statusKB is the value, in kB, of a field of /proc/pid/status, such as VmRSS or VmData. On
// macOS, which has no /proc, VmRSS is ps's resident size, and VmData, which it has no
// measure of, is 0.
func statusKB(t *testing.T, pid int, field string) int {
	t.Helper()
	if runtime.GOOS == "darwin" {
		if field == "VmRSS" {
			return residentKBOnMacOS(t, pid)
		}
		return 0
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	assert.NilError(t, err)
	for line := range strings.Lines(string(status)) {
		if value, found := strings.CutPrefix(line, field+":"); found {
			var kb int
			_, err := fmt.Sscanf(strings.TrimSpace(value), "%d kB", &kb)
			assert.NilError(t, err)
			return kb
		}
	}
	t.Fatalf("no %s in /proc/%d/status", field, pid)
	return 0
}

// TestHTTPSServerLifecycle runs http/lifecycle_tls.pts (H33b; D-163 over TLS) on one loop
// and on four, and a third time built with AddressSanitizer and UndefinedBehaviorSanitizer:
// clients that leave while their requests are held (closing, sending close_notify alone,
// or resetting) abort the requests' signals and their connections close at once, even if
// the handler ignores the signal; thousands of pipelined requests are answered in order;
// a client that doesn't read keeps the server from queueing more than about a megabyte of
// responses; a thousand TLS connections leave the files and the memory as they were (no
// session is kept); stop() from another loop lets a request in flight finish, closes idle
// connections with close_notify and refuses new ones; stop(true) from a pool thread closes
// an active connection, aborting its request's signal; then the program ends, with nothing
// on stderr. Under the sanitizers, only the memory it grows by isn't checked.
func TestHTTPSServerLifecycle(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "lifecycle_tls")
	source, err := filepath.Abs(filepath.Join("http", "lifecycle_tls.pts"))
	assert.NilError(t, err)
	sanitized := buildSanitized(t, source)
	certFile, keyFile, root := serverFiles(t, t.TempDir())
	config := &tls.Config{RootCAs: pool(root.cert)}
	for _, run := range []struct {
		threads   int
		sanitized bool
	}{{1, false}, {4, false}, {4, true}} {
		t.Run(fmt.Sprintf("PTS_THREADS=%d sanitized=%v", run.threads, run.sanitized), func(t *testing.T) {
			t.Parallel()
			env := []string{fmt.Sprintf("PTS_THREADS=%d", run.threads), "TLS_CERT_FILE=" + certFile, "TLS_KEY_FILE=" + keyFile}
			program := exe
			if run.sanitized {
				program = sanitized
				env = append(env, "ASAN_OPTIONS=detect_leaks=0:detect_stack_use_after_return=0")
			}
			server, ports := startServer(t, program, env...)
			checkHTTPSLifecycle(t, server, "127.0.0.1:"+ports[0], "127.0.0.1:"+ports[1], config, run.sanitized)
		})
	}
}

// checkHTTPSLifecycle talks to lifecycle_tls.pts running, whose servers are at main and
// second, as TestHTTPSServerLifecycle describes, with config's TLS client.
func checkHTTPSLifecycle(t *testing.T, server *httpServer, main string, second string, config *tls.Config, sanitized bool) {
	pid := server.cmd.Process.Pid
	expectLine := func(want string) {
		t.Helper()
		watchdog := time.AfterFunc(20*time.Second, func() { _ = server.cmd.Process.Kill() }) // so that a line that never comes fails the test
		defer watchdog.Stop()
		assert.Assert(t, server.lines.Scan(), "no line %q; stderr:\n%s", want, server.stderr)
		assert.Equal(t, server.lines.Text(), want)
	}
	files := openFiles(t, pid)
	sameFiles := func() bool { return openFiles(t, pid) == files }

	for _, client := range []struct {
		leaves string
		leave  func(*tlsConn)
	}{
		{"closes", func(c *tlsConn) { _ = c.Close() }},
		{"sends close_notify alone", func(c *tlsConn) { _ = c.CloseWrite() }}, // its half-close, which D-163 takes as leaving
		{"resets", func(c *tlsConn) {
			_ = c.tcp.Conn.(*net.TCPConn).SetLinger(0)
			_ = c.tcp.Close()
		}},
	} {
		held := dialTLS(t, main, config)
		held.send(t, "GET /hold HTTP/1.1\r\nHost: a\r\n\r\n")
		expectLine("holding")
		client.leave(held)
		expectLine("client left: AbortError")
		eventually(t, 2*time.Second, sameFiles, "the held request's connection closes when its client "+client.leaves)
		_ = held.Close()
	}

	ignored := dialTLS(t, main, config)
	ignored.send(t, "GET /ignore HTTP/1.1\r\nHost: a\r\n\r\n")
	expectLine("ignoring")
	_ = ignored.Close()
	eventually(t, time.Second, sameFiles, "the connection closes before the handler answers")
	expectLine("answered late: true")

	pipelined := dialTLS(t, main, config)
	go func() { _, _ = pipelined.Write([]byte(strings.Repeat("GET /tiny HTTP/1.1\r\nHost: a\r\n\r\n", 5000))) }()
	for i := range 5000 {
		response, body := pipelined.response(t)
		if response.StatusCode != 200 || body != "tiny" {
			t.Fatalf("pipelined response %d: %d %q", i, response.StatusCode, body)
		}
	}

	resident := residentMB(t, pid)
	slow := dialTLS(t, main, config)
	slow.send(t, strings.Repeat("GET /big HTTP/1.1\r\nHost: a\r\n\r\n", 64))
	time.Sleep(500 * time.Millisecond)
	grown := residentMB(t, pid) - resident
	assert.Assert(t, grown < 24 || sanitized, "64 unread 1 MB responses grew the server by %.1f MB", grown) // AddressSanitizer keeps freed memory
	for i := range 64 {
		_, body := slow.response(t)
		assert.Equal(t, len(body), 1024*1024, "response %d", i)
	}
	_ = pipelined.Close()
	_ = slow.Close()
	eventually(t, 2*time.Second, sameFiles, "the pipelining and slow clients' connections close")

	fresh := httpsClient(config.RootCAs, nil, false)
	_ = getAll(t, fresh, "https://"+main+"/tiny", 16, 10) // warm-up
	eventually(t, 2*time.Second, sameFiles, "the warm-up's connections close")
	rss, data := statusKB(t, pid, "VmRSS"), statusKB(t, pid, "VmData")
	bodies := getAll(t, fresh, "https://"+main+"/tiny", 16, 63)
	assert.DeepEqual(t, tallyBodies(bodies), map[string]int{"tiny": 16 * 63})
	eventually(t, 2*time.Second, sameFiles, "a thousand TLS connections close")
	grownRSS, grownData := float64(statusKB(t, pid, "VmRSS")-rss)/1024, float64(statusKB(t, pid, "VmData")-data)/1024
	// The plain server grows by nothing here after its warm-up. A session kept after its connection would be over
	// 33 kB of mbedTLS's record buffers, mostly never touched, so not resident: VmData counts it whole.
	assert.Assert(t, sanitized || (grownRSS < 12 && grownData < 16), "a thousand TLS connections grew the server by %.1f MB resident, %.1f MB of data", grownRSS, grownData)

	inFlight := dialTLS(t, main, config)
	inFlight.send(t, "GET /slow HTTP/1.1\r\nHost: a\r\n\r\n")
	expectLine("slow")
	idle := dialTLS(t, main, config)
	idle.send(t, "GET /tiny HTTP/1.1\r\nHost: a\r\n\r\n")
	_, body := idle.response(t)
	assert.Equal(t, body, "tiny")
	stopper := dialTLS(t, main, config)
	stopper.send(t, "GET /stop HTTP/1.1\r\nHost: a\r\n\r\n")
	expectLine("stopped")
	response, body := stopper.response(t)
	assert.Equal(t, body, "stopping")
	assert.Assert(t, response.Close, "the answer after stop() closes its connection")
	stopper.endedWithCloseNotify(t)
	response, body = inFlight.response(t)
	assert.Equal(t, body, "slow done")
	assert.Assert(t, response.Close, "a request in flight is answered, then its connection closes")
	assert.Equal(t, idle.rest(t), "", "stop() closes idle connections")
	idle.endedWithCloseNotify(t)
	_, err := net.Dial("tcp", main)
	assert.Check(t, cmp.ErrorContains(err, "connection refused"), "stop() closes the listener")

	active := dialTLS(t, second, config)
	active.send(t, "GET /hold HTTP/1.1\r\nHost: a\r\n\r\n")
	expectLine("holding on the second server")
	stopNow := dialTLS(t, second, config)
	stopNow.send(t, "GET /stop HTTP/1.1\r\nHost: a\r\n\r\n")
	_, body = stopNow.response(t)
	assert.Equal(t, body, "stopping now")
	expectLine("closed by stop(true): AbortError")
	assert.Assert(t, active.closedByServer(t), "stop(true) closes a connection whose request is in flight")
	awaitExit(t, server)
	assert.Equal(t, server.stderr.String(), "")
}

// ---------------------------------------------------------------- fetching itself

// fetchSelfTLSOutput is what http/fetch_self_tls.pts prints. One line ends in a space, after "list:".
const fetchSelfTLSOutput = `protocols: https https https
from the main loop: ok 32
from .par.map: ok 32
from parallel tasks: ok 32
request.url: <main>/url
https to https, another origin: 200 true <other>/echo GET 0 authorization=null
https to https, the same origin: 200 true <main>/echo POST 4 authorization=Bearer secret
1 MB posted: 200 false <main>/echo POST 1048576 authorization=null
a handler's fetch: 200 false <main>/proxy proxied 200 n proxied
by an address the certificate doesn't name: TypeError: fetch failed | ERR_TLS_CERT_ALTNAME_INVALID | Hostname/IP does not match certificate's altnames: IP: 127.0.0.1 is not in the cert's list: 
a self-signed server: TypeError: fetch failed | DEPTH_ZERO_SELF_SIGNED_CERT | self-signed certificate
stopped
`

// TestHTTPSServerFetchesItself runs http/fetch_self_tls.pts (H33b) on one loop and on
// four: a program whose fetch (H33a) is the client of its own HTTPS servers, from the
// main loop, `.par.map` callbacks, `parallel` tasks and a handler, with redirects between
// them, trusting their CA through NODE_EXTRA_CA_CERTS; the certificate names localhost
// alone, so a request by 127.0.0.1 fails as Node's does, and so does one to a server whose
// certificate is self-signed. It runs a third time built with AddressSanitizer and
// UndefinedBehaviorSanitizer, where client and server sessions share the loops.
func TestHTTPSServerFetchesItself(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "fetch_self_tls")
	source, err := filepath.Abs(filepath.Join("http", "fetch_self_tls.pts"))
	assert.NilError(t, err)
	sanitized := buildSanitized(t, source)
	dir := t.TempDir()
	root := newAuthority(t, "PTS Test Root CA", "p256", nil, nil)
	named := serverCert(t, root, "p256", func(c *x509.Certificate) { c.IPAddresses = nil })
	selfSigned := serverCert(t, nil, "p256", nil)
	env := []string{
		"NODE_EXTRA_CA_CERTS=" + writePEM(t, dir, "root.pem", root.cert),
		"TLS_CERT_FILE=" + writeFile(t, dir, "server.pem", chainPEM(named)),
		"TLS_KEY_FILE=" + writeFile(t, dir, "server-key.pem", keyPEM(t, signerOf(named), "pkcs8")),
		"SELF_SIGNED_CERT_FILE=" + writeFile(t, dir, "self-signed.pem", chainPEM(selfSigned)),
		"SELF_SIGNED_KEY_FILE=" + writeFile(t, dir, "self-signed-key.pem", keyPEM(t, signerOf(selfSigned), "sec1")),
	}
	for _, run := range []struct {
		threads string
		exe     string
	}{{"1", exe}, {"4", exe}, {"4", sanitized}} {
		got := runTLSProgram(t, append(env, "PTS_THREADS="+run.threads, "ASAN_OPTIONS=detect_leaks=0:detect_stack_use_after_return=0"), run.exe)
		label := fmt.Sprintf("PTS_THREADS=%s sanitized=%v", run.threads, run.exe == sanitized)
		assert.Equal(t, got.stdout, fetchSelfTLSOutput, "%s; stderr:\n%s", label, got.stderr)
		assert.Equal(t, got.exitCode, 0, "%s; stderr:\n%s", label, got.stderr)
		assert.Equal(t, got.stderr, "", label)
	}
}

// ---------------------------------------------------------------- options

// serveTLSOptionsOutput is what http/serve_tls_options.pts prints; "…" stands for
// mbedTLS's description and number of what it couldn't parse.
const serveTLSOptionsOutput = `nothing: TypeError (a TypeError): serve's tls option needs cert or certFile
a key alone: TypeError (a TypeError): serve's tls option needs cert or certFile
a certificate alone: TypeError (a TypeError): serve's tls option needs key or keyFile
a certificate file alone: TypeError (a TypeError): serve's tls option needs key or keyFile
cert and certFile: TypeError (a TypeError): serve's tls option takes cert or certFile, not both
key and keyFile: TypeError (a TypeError): serve's tls option takes key or keyFile, not both
certFile missing: Error: ENOENT: no such file or directory, open 'missing.pem' | ENOENT -2 open missing.pem | as readFileSync: true
keyFile missing: Error: ENOENT: no such file or directory, open 'missing-key.pem' | ENOENT -2 open missing-key.pem | as readFileSync: true
certFile missing, on every loop: Error: ENOENT: no such file or directory, open 'missing.pem' | ENOENT -2 open missing.pem | as readFileSync: true
certFile a directory: Error: EISDIR: illegal operation on a directory, read | EISDIR -21 read undefined | as readFileSync: true
cert not PEM: Error: serve's tls cert: … (mbedTLS -0x…)
cert a key: Error: serve's tls cert: … (mbedTLS -0x…)
cert an Ed25519 certificate: Error: serve's tls cert: … (mbedTLS -0x…)
key not PEM: Error: serve's tls key: … (mbedTLS -0x…)
key a certificate: Error: serve's tls key: … (mbedTLS -0x…)
encrypted key, no passphrase: Error: serve's tls key: … (mbedTLS -0x…)
encrypted key, wrong passphrase: Error: serve's tls key: … (mbedTLS -0x…)
legacy encrypted key, no passphrase: Error: serve's tls key: … (mbedTLS -0x…)
legacy encrypted key, wrong passphrase: Error: serve's tls key: … (mbedTLS -0x…)
another key: Error: serve's tls key doesn't match the certificate
an RSA certificate, a P-256 key: Error: serve's tls key doesn't match the certificate
the intermediate first: Error: serve's tls key doesn't match the certificate
a chain with a certificate that doesn't parse: Error: serve's tls cert: a certificate in it can't be parsed
a missing file at a port in use: Error: ENOENT: no such file or directory, open 'missing.pem' | ENOENT -2 open missing.pem | as readFileSync: true
a port in use: Error: listen EADDRINUSE: address already in use 127.0.0.1:PORT
certFile and keyFile: https true
cert and key: https true
undefined beside what is given: https true
a passphrase for a key that isn't encrypted: https true
encrypted key and its passphrase: https true
legacy encrypted key and its passphrase: https true
on every loop: https true
tls undefined: http true
on every address: https true
files as before: true
the port is free: true
`

// freePort is a port of 127.0.0.1 that nothing listens on (as far as the test knows).
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	defer listener.Close()
	return port(listener)
}

// TestHTTPSServerOptions runs http/serve_tls_options.pts (H33b) in a directory of its own:
// serve throws a TypeError for a tls option without a certificate or a key, or with both
// forms of one; Node's fs error, as readFileSync throws it, for a file it can't read; an
// Error with mbedTLS's description for a certificate or key mbedTLS can't parse (a wrong
// or missing passphrase among them) and its own for a key that isn't the certificate's;
// each before it listens, so that the port stays free and no file stays open, also with
// reusePort; and it serves HTTPS for each form that is right.
func TestHTTPSServerOptions(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "serve_tls_options")
	dir := t.TempDir()
	root := newAuthority(t, "PTS Test Root CA", "p256", nil, nil)
	intermediate := newAuthority(t, "PTS Test Intermediate CA", "p256", root, nil)
	chain := serverCert(t, intermediate, "p256", nil)
	_, ed25519Key, err := ed25519.GenerateKey(rand.Reader) // a key mbedTLS 3.6 has no algorithm for
	assert.NilError(t, err)
	ed25519Cert := certifyDER(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
	}, ed25519Key, root)
	directory := filepath.Join(dir, "a-directory")
	assert.NilError(t, os.Mkdir(directory, 0o755))
	reversed := append(chainPEM(tls.Certificate{Certificate: [][]byte{intermediate.cert.Raw}}), chainPEM(chain)...)
	env := []string{
		"FREE_PORT=" + freePort(t),
		"CERT_FILE=" + writeFile(t, dir, "cert.pem", chainPEM(chain)),
		"KEY_FILE=" + writeFile(t, dir, "key.pem", keyPEM(t, signerOf(chain), "pkcs8")),
		"OTHER_KEY_FILE=" + writeFile(t, dir, "other-key.pem", keyPEM(t, newKey(t, "p256"), "pkcs8")),
		"ENCRYPTED_KEY_FILE=" + writeFile(t, dir, "encrypted-key.pem", encryptedPKCS8PEM(t, signerOf(chain), tlsPassphrase)),
		"LEGACY_KEY_FILE=" + writeFile(t, dir, "legacy-key.pem", legacyEncryptedPEM(t, signerOf(chain), tlsPassphrase)),
		"PASSPHRASE=" + tlsPassphrase,
		"RSA_CERT_FILE=" + writeFile(t, dir, "rsa.pem", chainPEM(serverCert(t, root, "rsa", nil))),
		"ED25519_CERT_FILE=" + writeFile(t, dir, "ed25519.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ed25519Cert})),
		"REVERSED_CHAIN_FILE=" + writeFile(t, dir, "reversed.pem", reversed),
		"BROKEN_CHAIN_FILE=" + writeFile(t, dir, "broken.pem", append(chainPEM(chain), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not DER")})...)),
		"DIRECTORY=" + directory,
	}
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	ctx, cancel := context.WithTimeout(t.Context(), programTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	assert.NilError(t, err, "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	assert.Equal(t, stdout.String(), serveTLSOptionsOutput, "stderr:\n%s", stderr.String())
	assert.Equal(t, stderr.String(), "")
}

// ---------------------------------------------------------------- clients that reset or close_notify

// sizedServer is http/sized.pts built once for the tests that run it, which run at once:
// building it again while another test runs it fails with ETXTBSY.
var sizedServer = sync.OnceValues(func() (string, error) {
	exe := filepath.Join(buildDir, "http_sized")
	source, err := filepath.Abs(filepath.Join("http", "sized.pts"))
	if err != nil {
		return "", err
	}
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	return exe, driver.Build(context.Background(), source, exe)
})

// buildSized is sized.pts's executable.
func buildSized(t *testing.T) string {
	t.Helper()
	exe, err := sizedServer()
	assert.NilError(t, err)
	return exe
}

// TestHTTPServersSurviveClientsThatResetMidPipeline runs sized.pts over HTTP and HTTPS
// against clients that pipeline a hundred requests for 100 KB answers and then reset
// their connections: the server writes on to a socket the client has reset, which must
// fail that connection only, not end the program by SIGPIPE (H33b's review, R1-F1;
// pre-existing over HTTP). The server then still answers, and ends after /stop with
// nothing on stderr.
func TestHTTPServersSurviveClientsThatResetMidPipeline(t *testing.T) {
	t.Parallel()
	exe := buildSized(t)
	certFile, keyFile, root := serverFiles(t, t.TempDir())
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", secure), func(t *testing.T) {
			t.Parallel()
			var env []string
			var config *tls.Config
			if secure {
				env = []string{"TLS_CERT_FILE=" + certFile, "TLS_KEY_FILE=" + keyFile}
				config = &tls.Config{RootCAs: pool(root.cert), ServerName: "localhost"}
			}
			server, ports := startServer(t, exe, env...)
			address := "127.0.0.1:" + ports[0]
			for range 20 {
				resetMidPipeline(t, address, config)
			}
			client, scheme := http.DefaultClient, "http"
			if secure {
				client, scheme = httpsClient(config.RootCAs, nil, false), "https"
			}
			_, body := requestWith(t, client, "GET", scheme+"://"+address+"/sized?5", "")
			assert.Equal(t, body, "xxxxx")
			_, body = requestWith(t, client, "GET", scheme+"://"+address+"/stop", "")
			assert.Equal(t, body, "bye")
			awaitExit(t, server)
			assert.Equal(t, server.stderr.String(), "")
		})
	}
}

// resetMidPipeline connects to address (over TLS if config isn't nil), pipelines a hundred
// requests for 100 KB answers, and resets the connection while the server is answering.
func resetMidPipeline(t *testing.T, address string, config *tls.Config) {
	t.Helper()
	tcp, err := net.DialTimeout("tcp", address, 5*time.Second)
	assert.NilError(t, err)
	var conn net.Conn = tcp
	if config != nil {
		secured := tls.Client(tcp, config)
		assert.NilError(t, secured.HandshakeContext(t.Context()))
		conn = secured
	}
	_, err = conn.Write([]byte(strings.Repeat("GET /sized?100000 HTTP/1.1\r\nHost: a\r\n\r\n", 100)))
	assert.NilError(t, err)
	time.Sleep(20 * time.Millisecond)
	assert.NilError(t, tcp.(*net.TCPConn).SetLinger(0))
	assert.NilError(t, tcp.Close())
}

// TestHTTPSServerAnswersCloseNotify: a client that ends its side of an idle connection with
// close_notify, keeping TCP open, gets the server's close_notify before the TCP close, as
// RFC 8446 §6.1 and RFC 5246 §7.2.1 ask and Node's server does (H33b's review, R1-F2).
func TestHTTPSServerAnswersCloseNotify(t *testing.T) {
	t.Parallel()
	exe := buildSized(t)
	certFile, keyFile, root := serverFiles(t, t.TempDir())
	server, ports := startServer(t, exe, "TLS_CERT_FILE="+certFile, "TLS_KEY_FILE="+keyFile)
	address := "127.0.0.1:" + ports[0]
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		conn := dialTLS(t, address, &tls.Config{RootCAs: pool(root.cert), ServerName: "localhost", MinVersion: version, MaxVersion: version})
		conn.send(t, "GET /sized?3 HTTP/1.1\r\nHost: a\r\n\r\n")
		_, body := conn.response(t)
		assert.Equal(t, body, "xxx")
		assert.NilError(t, conn.CloseWrite())
		conn.endedWithCloseNotify(t)
	}
	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		closesAndNotifiesAtOnce(t, address, &tls.Config{RootCAs: pool(root.cert), ServerName: "localhost", MinVersion: version, MaxVersion: version})
	}
	_, body := requestWith(t, httpsClient(pool(root.cert), nil, false), "GET", "https://"+address+"/stop", "")
	assert.Equal(t, body, "bye")
	awaitExit(t, server)
	assert.Equal(t, server.stderr.String(), "")
}

// heldConn is a TCP connection whose writes, while held, wait in a buffer until released,
// so that two TLS records reach the server in one segment.
type heldConn struct {
	net.Conn
	holding bool
	held    []byte
}

func (c *heldConn) Write(p []byte) (int, error) {
	if c.holding {
		c.held = append(c.held, p...)
		return len(p), nil
	}
	return c.Conn.Write(p)
}

// closesAndNotifiesAtOnce sends a request whose answer closes the connection and the
// client's close_notify in one segment, so that the server reads both at once: the answer
// ends with one close_notify, not a second for the client's (H33b's review, R2-F1; checked
// under TLS 1.2, whose records show their type).
func closesAndNotifiesAtOnce(t *testing.T, address string, config *tls.Config) {
	t.Helper()
	tcp, err := net.DialTimeout("tcp", address, 5*time.Second)
	assert.NilError(t, err)
	held := &heldConn{Conn: tcp}
	recording := &recordingConn{Conn: held}
	secured := tls.Client(recording, config)
	t.Cleanup(func() { _ = secured.Close() })
	assert.NilError(t, secured.HandshakeContext(t.Context()))
	conn := &tlsConn{Conn: secured, tcp: recording, reader: bufio.NewReader(secured)}
	held.holding = true
	conn.send(t, "GET /sized?3 HTTP/1.1\r\nHost: a\r\nConnection: close\r\n\r\n")
	assert.NilError(t, conn.CloseWrite())
	held.holding = false
	assert.NilError(t, tcp.SetWriteDeadline(time.Time{})) // CloseWrite leaves the deadline at now
	_, err = tcp.Write(held.held)
	assert.NilError(t, err)
	_, body := conn.response(t)
	assert.Equal(t, body, "xxx")
	conn.endedWithCloseNotify(t)
	if config.MaxVersion == tls.VersionTLS12 { // TLS 1.3 hides a record's type
		recording.mu.Lock()
		defer recording.mu.Unlock()
		alerts := 0
		for _, r := range recording.records {
			if r.contentType == 21 {
				alerts++
			}
		}
		assert.Equal(t, alerts, 1, "records: %+v", recording.records)
	}
}

// TestHTTPSServerDecryptsItsKeyOnce runs encrypted_key_loops.pts on eight loops with an RSA
// key encrypted at 300,000 PBKDF2 iterations: starting its server on every loop takes less
// than three times as long as starting one on one loop, since the key is decrypted once,
// not once per loop (H33b's review, R1-F3: eight times as long); fresh connections, which
// the kernel spreads over the loops, all get answers, so each loop's copy of the RSA key
// serves; and the program ends after /stop, with nothing on stderr.
func TestHTTPSServerDecryptsItsKeyOnce(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "encrypted_key_loops")
	dir := t.TempDir()
	root := newAuthority(t, "PTS Test Root CA", "p256", nil, nil)
	chain := serverCert(t, root, "rsa", nil)
	certFile := writeFile(t, dir, "server.pem", chainPEM(chain))
	keyFile := writeFile(t, dir, "server-key.pem", encryptedPKCS8PEMIterated(t, signerOf(chain), "slow secret", 300_000))
	server, fields := startServer(t, exe, "PTS_THREADS=8", "TLS_CERT_FILE="+certFile, "TLS_KEY_FILE="+keyFile, "TLS_PASSPHRASE=slow secret")
	assert.Equal(t, len(fields), 3, server.lines.Text())
	slower, err := strconv.ParseFloat(fields[2], 64)
	assert.NilError(t, err)
	assert.Assert(t, slower < 3, "the server on every loop took %.2f times as long to start as the one on one loop", slower)
	t.Logf("the server on every loop took %.2f times as long to start as the one on one loop", slower)
	fresh := httpsClient(pool(root.cert), nil, false)
	bodies := getAll(t, fresh, "https://127.0.0.1:"+fields[0]+"/", 8, 8)
	assert.DeepEqual(t, tallyBodies(bodies), map[string]int{"every loop": 64})
	_, body := requestWith(t, fresh, "GET", "https://127.0.0.1:"+fields[1]+"/", "")
	assert.Equal(t, body, "one loop")
	_, body = requestWith(t, fresh, "GET", "https://127.0.0.1:"+fields[0]+"/stop", "")
	assert.Equal(t, body, "bye")
	awaitExit(t, server)
	assert.Equal(t, server.stderr.String(), "")
}

// TestHTTPSServerSendsNothingAfterAFatalAlert: a malformed request and a record that doesn't
// decrypt, read at once, make mbedTLS write its fatal alert (bad_record_mac) while the
// requests are decrypted; the server's 400 for the request must not follow it, since a
// connection is closed once a fatal alert is sent (RFC 8446 §6.2; H33b's review, R3-F1).
// Checked under TLS 1.2, whose records show their type: the last records are the alert alone.
func TestHTTPSServerSendsNothingAfterAFatalAlert(t *testing.T) {
	t.Parallel()
	exe := buildSized(t)
	certFile, keyFile, root := serverFiles(t, t.TempDir())
	server, ports := startServer(t, exe, "TLS_CERT_FILE="+certFile, "TLS_KEY_FILE="+keyFile)
	address := "127.0.0.1:" + ports[0]
	tcp, err := net.DialTimeout("tcp", address, 5*time.Second)
	assert.NilError(t, err)
	defer tcp.Close()
	held := &heldConn{Conn: tcp}
	secured := tls.Client(held, &tls.Config{RootCAs: pool(root.cert), ServerName: "localhost", MaxVersion: tls.VersionTLS12})
	assert.NilError(t, secured.HandshakeContext(t.Context()))
	held.holding = true
	_, err = secured.Write([]byte("NOT HTTP\r\n\r\n"))
	assert.NilError(t, err)
	garbage := append([]byte{23, 3, 3, 0, 48}, bytes.Repeat([]byte{0x5a}, 48)...) // application data that doesn't decrypt
	_, err = tcp.Write(append(held.held, garbage...))
	assert.NilError(t, err)
	assert.NilError(t, tcp.SetReadDeadline(time.Now().Add(5*time.Second)))
	sent, err := io.ReadAll(tcp)
	assert.Assert(t, !isTimeout(err), "the server didn't end the connection")
	var types []byte
	for rest := sent; len(rest) >= 5; {
		length := int(rest[3])<<8 | int(rest[4])
		types = append(types, rest[0])
		rest = rest[min(len(rest), 5+length):]
	}
	assert.DeepEqual(t, types, []byte{21})
	_, body := requestWith(t, httpsClient(pool(root.cert), nil, false), "GET", "https://"+address+"/stop", "")
	assert.Equal(t, body, "bye")
	awaitExit(t, server)
	assert.Equal(t, server.stderr.String(), "")
}
