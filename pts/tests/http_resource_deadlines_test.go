package tests

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

const (
	resourceHeadersTimeout   = 400 * time.Millisecond
	resourceHandshakeTimeout = 600 * time.Millisecond
	resourceObservation      = 3 * time.Second
	resourcePhasePause       = 900 * time.Millisecond
	resourceTrickleInterval  = 80 * time.Millisecond
	resourceResponseLimit    = 64 * 1024
	resourceLargeBodySize    = 4 * 1024 * 1024
	resourceIncompleteHead   = "GET /ok HTTP/1.1\r\nHost: loopback\r\n"
)

func buildResourceDeadlineServer(t *testing.T) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("http", "resource_deadlines.pts"))
	assert.NilError(t, err)
	entry, _ := builds.LoadOrStore(source, &programBuild{})
	build := entry.(*programBuild)
	build.once.Do(func() {
		heavyJobs <- struct{}{}
		defer func() { <-heavyJobs }()
		build.exe = filepath.Join(buildDir, "http_resource_deadlines")
		build.err = driver.Build(context.Background(), source, build.exe)
	})
	assert.NilError(t, build.err)
	return build.exe
}

type resourceEndpoint struct {
	server    *httpServer
	address   string
	base      string
	tlsConfig *tls.Config
	client    *http.Client
	exited    chan error
}

func startResourceEndpoint(t *testing.T, exe string, secure bool, threads int, headers time.Duration, handshake time.Duration) resourceEndpoint {
	t.Helper()
	env := []string{
		fmt.Sprintf("PTS_THREADS=%d", threads),
		"H14_REUSE_PORT=1",
		fmt.Sprintf("H14_HEADERS_TIMEOUT=%d", headers.Milliseconds()),
		fmt.Sprintf("H14_HANDSHAKE_TIMEOUT=%d", handshake.Milliseconds()),
	}
	protocol := "http"
	var config *tls.Config
	if secure {
		protocol = "https"
		certFile, keyFile, root := serverFiles(t, t.TempDir())
		env = append(env, "H14_TLS_CERT_FILE="+certFile, "H14_TLS_KEY_FILE="+keyFile)
		config = &tls.Config{RootCAs: pool(root.cert), ServerName: "127.0.0.1", NextProtos: []string{"http/1.1"}}
	}
	server, ports := startServer(t, exe, env...)
	assert.Equal(t, len(ports), 1)
	endpoint := resourceEndpoint{
		server: server, address: "127.0.0.1:" + ports[0], tlsConfig: config,
		client: &http.Client{Transport: &http.Transport{TLSClientConfig: config, DisableKeepAlives: true}, Timeout: resourceObservation},
		exited: make(chan error, 1),
	}
	endpoint.base = protocol + "://" + endpoint.address
	go func() { endpoint.exited <- server.cmd.Wait() }()
	t.Cleanup(endpoint.client.CloseIdleConnections)
	return endpoint
}

func (endpoint resourceEndpoint) stop(t *testing.T) {
	t.Helper()
	response, err := endpoint.client.Get(endpoint.base + "/stop")
	if err == nil {
		_, err = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	if err != nil {
		t.Errorf("stop request: %v", err)
	}
	select {
	case err := <-endpoint.exited:
		assert.Check(t, err == nil, "server exit: %v; stderr=%s", err, endpoint.server.stderr)
	case <-time.After(resourceObservation):
		_ = endpoint.server.cmd.Process.Kill()
		<-endpoint.exited
		t.Error("the server did not exit after stop(true); a pre-dispatch connection or timer may remain")
	}
	assert.Check(t, endpoint.server.stderr.String() == "", "server stderr=%s", endpoint.server.stderr)
}

func (endpoint resourceEndpoint) raw(t *testing.T) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", endpoint.address, resourceObservation)
	assert.NilError(t, err)
	assert.NilError(t, conn.SetDeadline(time.Now().Add(resourceObservation)))
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (endpoint resourceEndpoint) dial(t *testing.T) net.Conn {
	t.Helper()
	conn := endpoint.raw(t)
	if endpoint.tlsConfig == nil {
		return conn
	}
	secured := tls.Client(conn, endpoint.tlsConfig)
	assert.NilError(t, secured.HandshakeContext(t.Context()))
	assert.Assert(t, len(secured.ConnectionState().VerifiedChains) > 0, "HTTPS must verify the throwaway root")
	return secured
}

func (endpoint resourceEndpoint) get(t *testing.T, path string) string {
	t.Helper()
	response, body := requestWith(t, endpoint.client, "GET", endpoint.base+path, "")
	assert.Equal(t, response.StatusCode, http.StatusOK)
	return body
}

func (endpoint resourceEndpoint) count(t *testing.T) int {
	t.Helper()
	count, err := strconv.Atoi(endpoint.get(t, "/count"))
	assert.NilError(t, err)
	return count
}

type resourceRead struct {
	bytes   []byte
	err     error
	elapsed time.Duration
}

func readResourceClosure(conn io.Reader, started time.Time) resourceRead {
	bytes, err := io.ReadAll(io.LimitReader(conn, resourceResponseLimit))
	return resourceRead{bytes: bytes, err: err, elapsed: time.Since(started)}
}

func resourceConnectionClosed(err error) bool {
	return err == nil || errors.Is(err, syscall.ECONNRESET)
}

func TestResourceClosureAcceptsEOFOrResetOnly(t *testing.T) {
	for _, test := range []struct {
		name   string
		end    error
		closed bool
	}{
		{name: "EOF", end: io.EOF, closed: true},
		{name: "reset", end: syscall.ECONNRESET, closed: true},
		{
			name: "wrapped_read_reset",
			end: &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{
				Syscall: "read", Err: syscall.ECONNRESET,
			}},
			closed: true,
		},
		{name: "deadline", end: os.ErrDeadlineExceeded},
		{name: "wrapped_read_deadline", end: &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}},
		{name: "timeout", end: syscall.ETIMEDOUT},
		{name: "local_close", end: net.ErrClosed},
		{name: "unexpected_EOF", end: io.ErrUnexpectedEOF},
		{name: "broken_pipe", end: syscall.EPIPE},
		{name: "unrelated_error", end: errors.New("unrelated read failure")},
		{name: "reset_message_is_not_errno", end: errors.New("connection reset by peer")},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := readResourceClosure(iotest.ErrReader(test.end), time.Now())
			assert.Equal(t, len(got.bytes), 0)
			assert.Equal(t, resourceConnectionClosed(got.err), test.closed, "terminal read error: %v", got.err)
		})
	}
}

func checkResourceHeaderTimeout(t *testing.T, endpoint resourceEndpoint, conn net.Conn, started time.Time, trickle bool) {
	t.Helper()
	before := endpoint.count(t)
	var got resourceRead
	if trickle {
		got = trickleUntilResourceClosure(t, conn, started, func() []byte { return []byte("X-Progress: yes\r\n") })
	} else {
		got = readResourceClosure(conn, started)
	}
	t.Logf("header deadline: elapsed=%s wire=%q read_error=%v", got.elapsed, got.bytes, got.err)
	assert.Check(t, resourceConnectionClosed(got.err), "header expiration must close, not merely go quiet: %v", got.err)
	assert.Check(t, got.elapsed >= resourceHeadersTimeout/2, "headersTimeout is milliseconds, not an immediate close: %s", got.elapsed)
	assert.Check(t, len(got.bytes) < resourceResponseLimit, "response exceeded the bounded reader")
	assert.Check(t, len(got.bytes) == 0, "header expiration closes without an HTTP response: %q", got.bytes)
	assert.Check(t, got.elapsed < 2*time.Second, "absolute header deadline was not enforced: %s", got.elapsed)
	assert.Check(t, endpoint.count(t) == before, "an expired incomplete request must never reach the handler")
}

func trickleUntilResourceClosure(t *testing.T, conn net.Conn, started time.Time, progress func() []byte) resourceRead {
	t.Helper()
	done := make(chan resourceRead, 1)
	go func() { done <- readResourceClosure(conn, started) }()
	ticker := time.NewTicker(resourceTrickleInterval)
	defer ticker.Stop()
	limit := time.NewTimer(resourceObservation)
	defer limit.Stop()
	for {
		select {
		case got := <-done:
			return got
		case <-ticker.C:
			_, _ = conn.Write(progress()) // the read distinguishes a close from a buffered successful write
		case <-limit.C:
			_ = conn.Close()
			return <-done
		}
	}
}

func resourceRoundTrip(t *testing.T, conn net.Conn, reader *bufio.Reader, path string, body string) string {
	t.Helper()
	_, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: loopback\r\nContent-Length: %d\r\n\r\n%s", path, len(body), body)
	assert.NilError(t, err)
	response, err := http.ReadResponse(reader, nil)
	assert.NilError(t, err)
	assert.Equal(t, response.StatusCode, http.StatusOK)
	bytes, err := io.ReadAll(response.Body)
	assert.NilError(t, err)
	assert.NilError(t, response.Body.Close())
	return string(bytes)
}

func readResourceLargeResponse(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	assert.NilError(t, conn.SetReadDeadline(time.Now().Add(2*resourceObservation)))
	response, err := http.ReadResponse(reader, nil)
	assert.NilError(t, err)
	assert.Equal(t, response.StatusCode, http.StatusOK)
	body, err := io.ReadAll(io.LimitReader(response.Body, resourceLargeBodySize+1))
	assert.NilError(t, err)
	assert.NilError(t, response.Body.Close())
	assert.Equal(t, len(body), resourceLargeBodySize)
	assert.Assert(t, strings.Trim(string(body), "r") == "", "large response body changed")
}

func pauseResourcePhase(t *testing.T) {
	t.Helper()
	timer := time.NewTimer(resourcePhasePause)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
}

// These are bounded resource-policy tests, not malformed-line or chunk-framing tests:
// every transmitted HTTP line is ordinary valid HTTP, with completion merely delayed.
func TestHTTPServerPreDispatchDeadlines(t *testing.T) {
	t.Parallel()
	exe := buildResourceDeadlineServer(t)
	for _, threads := range []int{1, 4} {
		for _, secure := range []bool{false, true} {
			t.Run(fmt.Sprintf("threads=%d/TLS=%v", threads, secure), func(t *testing.T) {
				endpoint := startResourceEndpoint(t, exe, secure, threads, resourceHeadersTimeout, resourceHandshakeTimeout)
				defer endpoint.stop(t)
				assert.Equal(t, endpoint.get(t, "/ok"), "ok")

				t.Run("initial_silent_connection", func(t *testing.T) {
					conn := endpoint.dial(t)
					defer conn.Close()
					checkResourceHeaderTimeout(t, endpoint, conn, time.Now(), false)
				})
				t.Run("incomplete_headers", func(t *testing.T) {
					conn := endpoint.dial(t)
					defer conn.Close()
					started := time.Now()
					_, err := io.WriteString(conn, resourceIncompleteHead)
					assert.NilError(t, err)
					checkResourceHeaderTimeout(t, endpoint, conn, started, false)
				})
				t.Run("trickle_does_not_extend_absolute_deadline", func(t *testing.T) {
					conn := endpoint.dial(t)
					defer conn.Close()
					started := time.Now()
					_, err := io.WriteString(conn, resourceIncompleteHead)
					assert.NilError(t, err)
					checkResourceHeaderTimeout(t, endpoint, conn, started, true)
				})
				t.Run("next_request_has_its_own_header_deadline", func(t *testing.T) {
					conn := endpoint.dial(t)
					defer conn.Close()
					reader := bufio.NewReader(conn)
					assert.Equal(t, resourceRoundTrip(t, conn, reader, "/ok", ""), "ok")
					started := time.Now()
					_, err := io.WriteString(conn, resourceIncompleteHead)
					assert.NilError(t, err)
					assert.Equal(t, reader.Buffered(), 0)
					checkResourceHeaderTimeout(t, endpoint, conn, started, false)
				})
				t.Run("complete_headers_leave_body_phase_unlimited", func(t *testing.T) {
					conn := endpoint.dial(t)
					defer conn.Close()
					_, err := io.WriteString(conn, "POST /echo HTTP/1.1\r\nHost: loopback\r\nContent-Length: 2\r\n\r\na")
					assert.NilError(t, err)
					pauseResourcePhase(t)
					_, err = io.WriteString(conn, "b")
					assert.NilError(t, err)
					response, err := http.ReadResponse(bufio.NewReader(conn), nil)
					assert.NilError(t, err)
					assert.Equal(t, response.StatusCode, http.StatusOK)
					body, err := io.ReadAll(response.Body)
					assert.NilError(t, err)
					assert.NilError(t, response.Body.Close())
					assert.Equal(t, string(body), "ab")
				})
				t.Run("header_deadline_does_not_cancel_dispatched_handler", func(t *testing.T) {
					assert.Equal(t, endpoint.get(t, "/slow"), "slow:false")
				})
				t.Run("header_deadline_is_not_keepalive_idle_timeout", func(t *testing.T) {
					conn := endpoint.dial(t)
					defer conn.Close()
					reader := bufio.NewReader(conn)
					assert.Equal(t, resourceRoundTrip(t, conn, reader, "/ok", ""), "ok")
					pauseResourcePhase(t)
					assert.Equal(t, resourceRoundTrip(t, conn, reader, "/ok", ""), "ok")
				})
				t.Run("header_deadline_does_not_cut_queued_response", func(t *testing.T) {
					conn := endpoint.dial(t)
					defer conn.Close()
					_, err := io.WriteString(conn, "GET /large HTTP/1.1\r\nHost: loopback\r\n\r\n")
					assert.NilError(t, err)
					pauseResourcePhase(t)
					readResourceLargeResponse(t, conn, bufio.NewReader(conn))
				})
				t.Run("header_deadline_waits_for_queued_output_before_next_partial_request", func(t *testing.T) {
					before := endpoint.count(t)
					conn := endpoint.dial(t)
					defer conn.Close()
					_, err := io.WriteString(conn, "GET /large HTTP/1.1\r\nHost: loopback\r\n\r\n"+resourceIncompleteHead)
					assert.NilError(t, err)
					pauseResourcePhase(t)
					reader := bufio.NewReader(conn)
					readResourceLargeResponse(t, conn, reader)
					assert.Equal(t, reader.Buffered(), 0)
					assert.NilError(t, conn.SetReadDeadline(time.Now().Add(resourceObservation)))
					got := readResourceClosure(conn, time.Now())
					t.Logf("next partial header after output drain: elapsed=%s wire=%q read_error=%v", got.elapsed, got.bytes, got.err)
					assert.Check(t, resourceConnectionClosed(got.err), "the partial next request must close after output drains: %v", got.err)
					assert.Check(t, len(got.bytes) == 0, "no extra response follows the complete large answer")
					assert.Check(t, got.elapsed < 2*time.Second, "the next header deadline was never started")
					assert.Equal(t, endpoint.count(t), before+1, "only the complete large request may dispatch")
				})
				assert.Equal(t, endpoint.get(t, "/ok"), "ok", "timeouts must not stop the listener")
			})
		}
	}
}

type resourceHelloRecorder struct {
	net.Conn
	hello []byte
}

func (recorder *resourceHelloRecorder) Write(bytes []byte) (int, error) {
	recorder.hello = append([]byte(nil), bytes...)
	return 0, io.ErrClosedPipe
}

func resourceClientHello(t *testing.T, config *tls.Config) []byte {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	recorder := &resourceHelloRecorder{Conn: client}
	err := tls.Client(recorder, config).HandshakeContext(t.Context())
	assert.Assert(t, errors.Is(err, io.ErrClosedPipe), "capture the genuine ClientHello before any server response: %v", err)
	assert.Assert(t, len(recorder.hello) >= 64, "a genuine ClientHello must cover the bounded trickle: %d bytes", len(recorder.hello))
	return recorder.hello
}

func TestHTTPSServerHandshakeDeadline(t *testing.T) {
	t.Parallel()
	exe := buildResourceDeadlineServer(t)
	for _, threads := range []int{1, 4} {
		t.Run(fmt.Sprintf("threads=%d", threads), func(t *testing.T) {
			endpoint := startResourceEndpoint(t, exe, true, threads, 0, resourceHandshakeTimeout)
			defer endpoint.stop(t)
			for _, trickle := range []bool{false, true} {
				t.Run(fmt.Sprintf("trickle=%v", trickle), func(t *testing.T) {
					conn := endpoint.raw(t)
					defer conn.Close()
					started := time.Now()
					var got resourceRead
					if trickle {
						hello := resourceClientHello(t, endpoint.tlsConfig)
						_, err := conn.Write(hello[:5])
						assert.NilError(t, err)
						next := 5
						got = trickleUntilResourceClosure(t, conn, started, func() []byte {
							part := hello[next : next+1]
							next++
							return part
						})
					} else {
						got = readResourceClosure(conn, started)
					}
					t.Logf("handshake deadline: elapsed=%s bytes=%d read_error=%v", got.elapsed, len(got.bytes), got.err)
					assert.Check(t, resourceConnectionClosed(got.err), "an unfinished TLS handshake must close, not merely go quiet: %v", got.err)
					assert.Check(t, got.elapsed >= resourceHandshakeTimeout/2, "handshakeTimeout uses milliseconds: %s", got.elapsed)
					assert.Check(t, got.elapsed < 2*time.Second, "absolute handshake deadline was not enforced: %s", got.elapsed)
					assert.Check(t, len(got.bytes) == 0, "a handshake timeout must not write a plaintext HTTP response")
					assert.Equal(t, endpoint.count(t), 0)
				})
			}
			assert.Equal(t, endpoint.get(t, "/ok"), "ok", "an ordinary verified handshake must still complete")
		})
	}
}

func TestHTTPServerPreDispatchDeadlinesCanBeDisabled(t *testing.T) {
	t.Parallel()
	exe := buildResourceDeadlineServer(t)
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS=%v", secure), func(t *testing.T) {
			endpoint := startResourceEndpoint(t, exe, secure, 1, 0, 0)
			defer endpoint.stop(t)
			conn := endpoint.raw(t)
			defer conn.Close()
			if secure {
				pauseResourcePhase(t)
				secured := tls.Client(conn, endpoint.tlsConfig)
				assert.NilError(t, secured.HandshakeContext(t.Context()))
				assert.Assert(t, len(secured.ConnectionState().VerifiedChains) > 0)
				conn = secured
			}
			_, err := io.WriteString(conn, resourceIncompleteHead)
			assert.NilError(t, err)
			pauseResourcePhase(t)
			_, err = io.WriteString(conn, "\r\n")
			assert.NilError(t, err)
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			assert.NilError(t, err)
			assert.Equal(t, response.StatusCode, http.StatusOK)
			body, err := io.ReadAll(response.Body)
			assert.NilError(t, err)
			assert.NilError(t, response.Body.Close())
			assert.Equal(t, string(body), "ok")
		})
	}
}

func TestHTTPSServerStartsHeaderDeadlineAfterHandshake(t *testing.T) {
	t.Parallel()
	exe := buildResourceDeadlineServer(t)
	endpoint := startResourceEndpoint(t, exe, true, 1, resourceHeadersTimeout, 2*resourceObservation)
	defer endpoint.stop(t)
	conn := endpoint.raw(t)
	defer conn.Close()
	pauseResourcePhase(t)
	secured := tls.Client(conn, endpoint.tlsConfig)
	assert.NilError(t, secured.HandshakeContext(t.Context()))
	assert.Assert(t, len(secured.ConnectionState().VerifiedChains) > 0)
	assert.Equal(t, resourceRoundTrip(t, secured, bufio.NewReader(secured), "/ok", ""), "ok")
}

func awaitResourceState(t *testing.T, endpoint resourceEndpoint, want string) {
	t.Helper()
	deadline := time.NewTimer(resourceObservation)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if endpoint.get(t, "/held") == want {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("held request state never became %s", want)
		}
	}
}

func TestHTTPServerDeadlineTimersLeaveDisconnectSignalsWorking(t *testing.T) {
	t.Parallel()
	exe := buildResourceDeadlineServer(t)
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS=%v", secure), func(t *testing.T) {
			endpoint := startResourceEndpoint(t, exe, secure, 4, resourceHeadersTimeout, resourceHandshakeTimeout)
			defer endpoint.stop(t)
			for count := 1; count <= 4; count++ {
				conn := endpoint.dial(t)
				_, err := io.WriteString(conn, "GET /until-abort HTTP/1.1\r\nHost: loopback\r\n\r\n")
				assert.NilError(t, err)
				awaitResourceState(t, endpoint, fmt.Sprintf("%d:%d", count, count-1))
				if count == 1 {
					pauseResourcePhase(t)
					assert.Equal(t, endpoint.get(t, "/held"), "1:0", "a dispatched request is not a pending header")
				}
				assert.NilError(t, conn.Close())
				awaitResourceState(t, endpoint, fmt.Sprintf("%d:%d", count, count))
			}
		})
	}
}

func awaitResourceFiles(t *testing.T, pid int, baseline int) {
	t.Helper()
	deadline := time.NewTimer(resourceObservation)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		files := openFiles(t, pid)
		if files <= baseline {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("open files did not return to baseline: before=%d after=%d", baseline, files)
		}
	}
}

func TestHTTPServerDeadlineOptionsRejectInvalidMilliseconds(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "resource_deadline_options")
	result := execute(t, ".", []string{"PTS_THREADS=1"}, exe)
	t.Logf("timeout option validation: exit=%d stdout=%q stderr=%q", result.exitCode, result.stdout, result.stderr)
	assert.Equal(t, result.exitCode, 0)
	assert.Equal(t, result.stderr, "")
	var expected []string
	for _, name := range []string{"handshakeTimeout", "headersTimeout"} {
		for _, label := range []string{"omitted", "zero", "negative-zero", "one", "maximum"} {
			expected = append(expected, name+" "+label+" accepted")
		}
		for _, label := range []string{"negative", "fraction", "nan", "infinity", "negative-infinity", "above-maximum", "unsafe-integer"} {
			expected = append(expected, name+" "+label+" true true")
		}
	}
	assert.Equal(t, result.stdout, strings.Join(expected, "\n")+"\n")
}

func TestHTTPServerExpiredConnectionsReleaseResources(t *testing.T) {
	t.Parallel()
	exe := buildResourceDeadlineServer(t)
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS=%v", secure), func(t *testing.T) {
			endpoint := startResourceEndpoint(t, exe, secure, 4, resourceHeadersTimeout, resourceHandshakeTimeout)
			defer endpoint.stop(t)
			assert.Equal(t, endpoint.get(t, "/ok"), "ok")
			pid := endpoint.server.cmd.Process.Pid
			baselineFiles := openFiles(t, pid)
			baselineRSS := residentMB(t, pid)
			for batch := 0; batch < 4; batch++ {
				connections := make([]net.Conn, 0, 16)
				for range 16 {
					conn := endpoint.raw(t)
					if !secure {
						_, err := io.WriteString(conn, resourceIncompleteHead)
						assert.NilError(t, err)
					}
					connections = append(connections, conn)
				}
				for _, conn := range connections {
					got := readResourceClosure(conn, time.Now())
					assert.Check(t, resourceConnectionClosed(got.err), "batch=%d a connection stayed open past its deadline: %v", batch, got.err)
					_ = conn.Close()
				}
				awaitResourceFiles(t, pid, baselineFiles)
				assert.Equal(t, endpoint.get(t, "/ok"), "ok")
			}
			growth := residentMB(t, pid) - baselineRSS
			t.Logf("expired connections: TLS=%v files_before=%d files_after=%d rss_growth_mb=%.3f", secure, baselineFiles, openFiles(t, pid), growth)
			assert.Check(t, growth < 16, "64 expired connections retained too much resident memory: %.3f MiB", growth)
		})
	}
}
