package tests

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

const (
	framingDeadline    = 3 * time.Second
	framingQuietWindow = 40 * time.Millisecond
	framingBodyLimit   = 64 * 1024 * 1024
	framingMarker      = "GET /marker HTTP/1.1\r\nHost: loopback\r\nConnection: close\r\n\r\n"
)

type framingCase struct {
	Name       string `json:"name"`
	Headers    string `json:"headers"`
	Body       string `json:"body"`
	Status     int    `json:"status"`
	Decoded    string `json:"decoded"`
	HeaderOnly bool   `json:"headerOnly"`
}

type framingResponse struct {
	Status     int
	Body       string
	Connection string
}

type framingEndpoint struct {
	address   string
	tlsConfig *tls.Config
	client    *http.Client
}

func startFramingEndpoint(t *testing.T, exe string, secure bool) (*httpServer, framingEndpoint) {
	t.Helper()
	env := []string{"PTS_THREADS=1"}
	protocol := "http"
	var config *tls.Config
	if secure {
		protocol = "https"
		dir := t.TempDir()
		root := newAuthority(t, "PTS H13 Framing Root", "p256", nil, nil)
		certificate := serverCert(t, root, "p256", nil)
		env = append(env,
			"H13_TLS_CERT_FILE="+writeFile(t, dir, "framing.pem", chainPEM(certificate)),
			"H13_TLS_KEY_FILE="+writeFile(t, dir, "framing-key.pem", keyPEM(t, signerOf(certificate), "pkcs8")))
		config = &tls.Config{RootCAs: pool(root.cert), ServerName: "127.0.0.1", NextProtos: []string{"http/1.1"}}
	}
	server, ports := startServer(t, exe, env...)
	assert.Equal(t, len(ports), 1)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config, DisableKeepAlives: true}, Timeout: framingDeadline}
	t.Cleanup(client.CloseIdleConnections)
	return server, framingEndpoint{protocol + "://127.0.0.1:" + ports[0], config, client}
}

func (endpoint framingEndpoint) dial(t *testing.T) net.Conn {
	t.Helper()
	address := strings.TrimPrefix(strings.TrimPrefix(endpoint.address, "http://"), "https://")
	dialer := &net.Dialer{Timeout: framingDeadline}
	if endpoint.tlsConfig != nil {
		conn, err := tls.DialWithDialer(dialer, "tcp", address, endpoint.tlsConfig)
		assert.NilError(t, err)
		assert.Assert(t, len(conn.ConnectionState().VerifiedChains) > 0, "the throwaway CA must verify the HTTPS fixture")
		return conn
	}
	conn, err := dialer.Dial("tcp", address)
	assert.NilError(t, err)
	return conn
}

func (endpoint framingEndpoint) count(t *testing.T) int {
	t.Helper()
	response, body := requestWith(t, endpoint.client, "GET", endpoint.address+"/count", "")
	assert.Equal(t, response.StatusCode, http.StatusOK)
	count, err := strconv.Atoi(body)
	assert.NilError(t, err)
	return count
}

func framingCases(t *testing.T) []framingCase {
	t.Helper()
	data, err := os.ReadFile("http/framing_cases.json")
	assert.NilError(t, err)
	var cases []framingCase
	assert.NilError(t, json.Unmarshal(data, &cases))
	assert.Assert(t, len(cases) > 0)
	return cases
}

func framingRequest(headers string, body string) string {
	return "POST /probe HTTP/1.1\r\nHost: loopback\r\n" + headers + "\r\n" + body
}

// Unlike exchange(), a quiet connection is not evidence that the server closed it.
func framingExchange(t *testing.T, endpoint framingEndpoint, parts ...string) (string, error) {
	t.Helper()
	conn := endpoint.dial(t)
	defer conn.Close()
	assert.NilError(t, conn.SetDeadline(time.Now().Add(framingDeadline)))
	for i, part := range parts {
		_, err := io.WriteString(conn, part)
		assert.NilError(t, err)
		if i+1 < len(parts) {
			assert.NilError(t, conn.SetReadDeadline(time.Now().Add(framingQuietWindow)))
			var early [1]byte
			n, readErr := conn.Read(early[:])
			timeout, isNetError := readErr.(net.Error)
			assert.Assert(t, n == 0 && isNetError && timeout.Timeout(), "response before the final fragment: byte=%q error=%v", early[:n], readErr)
			assert.NilError(t, conn.SetReadDeadline(time.Now().Add(framingDeadline)))
		}
	}
	response, err := io.ReadAll(conn)
	return string(response), err
}

func framingResponses(t *testing.T, raw string) []framingResponse {
	t.Helper()
	source := strings.NewReader(raw)
	reader := bufio.NewReader(source)
	var responses []framingResponse
	for {
		if _, err := reader.Peek(1); err == io.EOF {
			return responses
		}
		// ReadResponse removes Connection: close from Header. Preserve the raw
		// field as well as checking its parsed close state, not just EOF.
		offset := len(raw) - source.Len() - reader.Buffered()
		headEnd := strings.Index(raw[offset:], "\r\n\r\n")
		if headEnd < 0 {
			t.Errorf("incomplete response head: raw=%q", raw)
			return responses
		}
		_, fields, _ := strings.Cut(raw[offset:offset+headEnd], "\r\n")
		headers, err := textproto.NewReader(bufio.NewReader(strings.NewReader(fields + "\r\n\r\n"))).ReadMIMEHeader()
		assert.NilError(t, err)
		connection := headers.Get("Connection")
		response, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Errorf("invalid response: %v; raw=%q", err, raw)
			return responses
		}
		body, err := io.ReadAll(response.Body)
		assert.NilError(t, err)
		assert.NilError(t, response.Body.Close())
		assert.Check(t, response.Close == strings.EqualFold(connection, "close"), "raw Connection header and parsed close state disagree: header=%q close=%v", connection, response.Close)
		responses = append(responses, framingResponse{response.StatusCode, string(body), connection})
	}
}

func checkFramingExchange(t *testing.T, endpoint framingEndpoint, status int, decoded string, parts ...string) {
	t.Helper()
	before := endpoint.count(t)
	raw, readErr := framingExchange(t, endpoint, parts...)
	t.Logf("wire=%q response=%q read_error=%v", strings.Join(parts, ""), raw, readErr)
	assert.Check(t, readErr == nil, "the server must close, not merely go quiet: %v", readErr)
	responses := framingResponses(t, raw)
	if status == http.StatusOK {
		assert.Check(t, cmp.DeepEqual(responses, []framingResponse{{http.StatusOK, "/probe:" + decoded, ""}, {http.StatusOK, "/marker:", "close"}}))
		assert.Check(t, cmp.Equal(endpoint.count(t)-before, 2), "both complete requests must be dispatched once")
		return
	}
	assert.Check(t, cmp.DeepEqual(responses, []framingResponse{{status, "", "close"}}), "no 100 Continue, successful response or pipelined marker may follow a framing rejection")
	assert.Check(t, cmp.Equal(endpoint.count(t), before), "neither the rejected request nor its pipelined marker may reach the handler")
}

func checkFramingFragments(t *testing.T, endpoint framingEndpoint) {
	t.Helper()
	t.Run("fragmented-content-length", func(t *testing.T) {
		checkFramingExchange(t, endpoint, http.StatusOK, "ab",
			"POST /probe HTTP/1.1\r\nHost: loopback\r\nContent-Len",
			"gth: 2\r\n\r\na", "b"+framingMarker)
	})
	t.Run("fragmented-chunked", func(t *testing.T) {
		checkFramingExchange(t, endpoint, http.StatusOK, "ab",
			framingRequest("Transfer-Encoding: chunked\r\n", "2\r"),
			"\nab\r\n0\r\n", "\r\n"+framingMarker)
	})
	t.Run("fragmented-cl-te", func(t *testing.T) {
		checkFramingExchange(t, endpoint, http.StatusBadRequest, "",
			"POST /probe HTTP/1.1\r\nHost: loopback\r\nContent-Length: 2\r\nTransfer-Enc",
			"oding: chunked\r\n\r\n2\r\nab\r\n0\r\n\r\n"+framingMarker)
	})
	t.Run("fragmented-duplicate-cl", func(t *testing.T) {
		checkFramingExchange(t, endpoint, http.StatusBadRequest, "",
			"POST /probe HTTP/1.1\r\nHost: loopback\r\nContent-Length: 2\r\nContent-Len",
			"gth: 2\r\n\r\nab"+framingMarker)
	})
	t.Run("rejection-after-valid-pipelined-request", func(t *testing.T) {
		before := endpoint.count(t)
		wire := "GET /before HTTP/1.1\r\nHost: loopback\r\n\r\n" + framingRequest("Content-Length: 0\r\nTransfer-Encoding: chunked\r\n", "0\r\n\r\n") + framingMarker
		raw, err := framingExchange(t, endpoint, wire)
		t.Logf("wire=%q response=%q read_error=%v", wire, raw, err)
		assert.Check(t, err == nil, "the rejected connection must reach EOF: %v", err)
		assert.Check(t, cmp.DeepEqual(framingResponses(t, raw), []framingResponse{{http.StatusOK, "/before:", ""}, {http.StatusBadRequest, "", "close"}}))
		assert.Check(t, cmp.Equal(endpoint.count(t)-before, 1))
	})
}

func checkFramingContinue(t *testing.T, endpoint framingEndpoint) {
	t.Helper()
	t.Run("valid-100-continue", func(t *testing.T) {
		before := endpoint.count(t)
		conn := endpoint.dial(t)
		defer conn.Close()
		assert.NilError(t, conn.SetDeadline(time.Now().Add(framingDeadline)))
		_, err := io.WriteString(conn, framingRequest("Content-Length: 2\r\nExpect: 100-continue\r\n", ""))
		assert.NilError(t, err)
		reader := bufio.NewReader(conn)
		interim, err := http.ReadResponse(reader, nil)
		assert.NilError(t, err)
		assert.Equal(t, interim.StatusCode, http.StatusContinue)
		assert.NilError(t, interim.Body.Close())
		_, err = io.WriteString(conn, "ab"+framingMarker)
		assert.NilError(t, err)
		raw, err := io.ReadAll(reader)
		assert.NilError(t, err, "the marker asks the connection to close")
		assert.DeepEqual(t, framingResponses(t, string(raw)), []framingResponse{{http.StatusOK, "/probe:ab", ""}, {http.StatusOK, "/marker:", "close"}})
		assert.Equal(t, endpoint.count(t)-before, 2)
	})
	t.Run("exact-body-limit-emits-continue-without-a-body", func(t *testing.T) {
		before := endpoint.count(t)
		conn := endpoint.dial(t)
		defer conn.Close()
		assert.NilError(t, conn.SetDeadline(time.Now().Add(framingDeadline)))
		_, err := io.WriteString(conn, framingRequest("Content-Length: "+strconv.Itoa(framingBodyLimit)+"\r\nExpect: 100-continue\r\n", ""))
		assert.NilError(t, err)
		response, err := http.ReadResponse(bufio.NewReader(conn), nil)
		assert.NilError(t, err)
		assert.Equal(t, response.StatusCode, http.StatusContinue)
		assert.NilError(t, response.Body.Close())
		assert.NilError(t, conn.Close())
		assert.Equal(t, endpoint.count(t), before, "an incomplete body must not reach the handler")
	})
}

func TestHTTPFramingRejectsAmbiguityAndKeepsValidBoundaries(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "framing")
	for _, secure := range []bool{false, true} {
		protocol := "http"
		if secure {
			protocol = "https"
		}
		t.Run(protocol, func(t *testing.T) {
			server, endpoint := startFramingEndpoint(t, exe, secure)
			for _, tc := range framingCases(t) {
				t.Run(tc.Name, func(t *testing.T) {
					wire := framingRequest(tc.Headers, tc.Body)
					if !tc.HeaderOnly {
						wire += framingMarker
					}
					checkFramingExchange(t, endpoint, tc.Status, tc.Decoded, wire)
				})
			}
			checkFramingFragments(t, endpoint)
			checkFramingContinue(t, endpoint)
			_, body := requestWith(t, endpoint.client, "GET", endpoint.address+"/stop", "")
			assert.Equal(t, body, "bye")
			awaitExit(t, server)
			assert.Equal(t, server.stderr.String(), "", "unexpected server diagnostics")
		})
	}
}
