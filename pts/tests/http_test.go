package tests

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

// httpServer is a running http/server.pts: its two servers' base URLs, its stdout and its process.
type httpServer struct {
	main, plain string
	lines       *bufio.Scanner
	stderr      *strings.Builder
	cmd         *exec.Cmd
}

// startHTTPServer builds and starts http/server.pts and reads the ports it listens on.
func startHTTPServer(t *testing.T) *httpServer {
	t.Helper()
	exe := filepath.Join(buildDir, "http_server")
	source, err := filepath.Abs(filepath.Join("http", "server.pts"))
	assert.NilError(t, err)
	heavyJobs <- struct{}{}
	err = driver.Build(context.Background(), source, exe)
	<-heavyJobs
	assert.NilError(t, err)
	cmd := exec.CommandContext(t.Context(), exe)
	stdout, err := cmd.StdoutPipe()
	assert.NilError(t, err)
	server := &httpServer{cmd: cmd, lines: bufio.NewScanner(stdout), stderr: &strings.Builder{}}
	cmd.Stderr = server.stderr
	assert.NilError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	assert.Assert(t, server.lines.Scan(), "no output: %s", server.stderr)
	fields := strings.Fields(server.lines.Text())
	assert.Equal(t, len(fields), 4, server.lines.Text())
	assert.Equal(t, fields[3], "true", "Server.url")
	server.main, server.plain = "http://127.0.0.1:"+fields[1], "http://127.0.0.1:"+fields[2]
	assert.Assert(t, server.lines.Scan())
	assert.Equal(t, server.lines.Text(), "listen EADDRINUSE: address already in use 127.0.0.1:PORT")
	return server
}

// request sends a request with Go's client (which keeps connections alive) and returns the response and its body.
func request(t *testing.T, method string, url string, body string, header ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	assert.NilError(t, err)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	response, err := http.DefaultClient.Do(req)
	assert.NilError(t, err)
	defer response.Body.Close()
	text, err := io.ReadAll(response.Body)
	assert.NilError(t, err)
	return response, string(text)
}

// exchange writes raw bytes to address and returns what comes back until the server closes the connection or goes quiet.
func exchange(t *testing.T, address string, raw string) string {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(address, "http://"))
	assert.NilError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte(raw))
	assert.NilError(t, err)
	assert.NilError(t, conn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
	got, _ := io.ReadAll(conn)
	return string(got)
}

// TestHTTPServer runs pts:http's server (D-136) and talks to it: routes, bodies, headers,
// keep-alive, pipelining, HTTP/1.0, chunked bodies, 100-continue, malformed requests,
// handlers that throw, and stop(), which ends the program.
func TestHTTPServer(t *testing.T) {
	t.Parallel()
	server := startHTTPServer(t)

	response, body := request(t, "GET", server.main+"/hello", "")
	assert.Equal(t, response.StatusCode, 200)
	assert.Equal(t, body, "hello")
	assert.Equal(t, response.Header.Get("Content-Type"), "text/plain;charset=UTF-8")

	response, body = request(t, "POST", server.main+"/echo", "ünïcode body", "Content-Type", "text/x-test")
	assert.Equal(t, body, "ünïcode body")
	assert.Equal(t, response.Header.Get("Content-Type"), "text/x-test")

	response, body = request(t, "POST", server.main+"/json?x=1", `{"list":[1,2],"n":null}`, "User-Agent", "pts-test")
	assert.Equal(t, response.Header.Get("Content-Type"), "application/json")
	assert.Equal(t, body, `{"method":"POST","path":"/json?x=1","body":{"list":[1,2],"n":null},"agent":"pts-test","port":`+strings.TrimPrefix(server.main, "http://127.0.0.1:")+`}`)

	response, body = request(t, "GET", server.main+"/status", "")
	assert.Equal(t, response.Status, "201 Made")
	assert.Equal(t, response.Header.Get("X-Test"), "a, b")
	assert.DeepEqual(t, response.Header.Values("Set-Cookie"), []string{"one=1", "two=2"})
	assert.Equal(t, body, "")

	response, _ = request(t, "HEAD", server.main+"/hello", "")
	assert.Equal(t, response.ContentLength, int64(5))
	_, body = request(t, "GET", server.main+"/later", "")
	assert.Equal(t, body, "later")
	response, body = request(t, "GET", server.main+"/missing", "")
	assert.Equal(t, response.StatusCode, 404)
	assert.Equal(t, body, "no such page")

	response, body = request(t, "GET", server.main+"/throw", "")
	assert.Equal(t, response.StatusCode, 503)
	assert.Equal(t, body, "handled: boom")
	response, body = request(t, "GET", server.plain+"/throw", "")
	assert.Equal(t, response.StatusCode, 500)
	assert.Equal(t, body, "Internal Server Error")
	_, body = request(t, "PUT", server.plain+"/x", "")
	assert.Equal(t, body, "plain PUT")

	pipelined := exchange(t, server.main, "GET /hello HTTP/1.1\r\nHost: a\r\n\r\nPOST /echo HTTP/1.1\r\nHost: a\r\nContent-Length: 3\r\n\r\nabcGET /hello HTTP/1.1\r\nHost: a\r\nConnection: close\r\n\r\n")
	assert.Equal(t, strings.Count(pipelined, "HTTP/1.1 200 OK"), 3, pipelined)
	assert.Assert(t, strings.Index(pipelined, "\r\n\r\nhello") < strings.Index(pipelined, "\r\n\r\nabc"), pipelined)
	assert.Assert(t, strings.HasSuffix(pipelined, "connection: close\r\n\r\nhello"), pipelined)

	old := exchange(t, server.main, "GET /hello HTTP/1.0\r\n\r\n")
	assert.Assert(t, strings.HasPrefix(old, "HTTP/1.1 200 OK\r\n") && strings.Contains(old, "connection: close\r\n") && strings.HasSuffix(old, "hello"), old)
	chunked := exchange(t, server.main, "POST /echo HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n4\r\nchun\r\n3;ext=1\r\nked\r\n0\r\n\r\n")
	assert.Assert(t, strings.HasSuffix(chunked, "content-length: 7\r\nconnection: close\r\n\r\nchunked"), chunked)
	continued := exchange(t, server.main, "POST /echo HTTP/1.1\r\nHost: a\r\nExpect: 100-continue\r\nContent-Length: 2\r\nConnection: close\r\n\r\n")
	assert.Equal(t, continued, "HTTP/1.1 100 Continue\r\n\r\n")
	malformed := exchange(t, server.main, "NOT HTTP\r\n\r\n")
	assert.Equal(t, malformed, "HTTP/1.1 400 Bad Request\r\ncontent-length: 0\r\nconnection: close\r\n\r\n")

	_, body = request(t, "GET", server.main+"/stop", "")
	assert.Equal(t, body, "bye")
	var log []string
	for server.lines.Scan() {
		log = append(log, server.lines.Text())
	}
	assert.NilError(t, server.cmd.Wait(), "stderr:\n%s", server.stderr)
	assert.DeepEqual(t, log, []string{"GET /hello", "POST /echo", "POST /json?x=1", "GET /status", "HEAD /hello", "GET /later", "GET /missing", "GET /throw",
		"GET /hello", "POST /echo", "GET /hello", "GET /hello", "POST /echo", "GET /stop"})
	assert.Assert(t, strings.Contains(server.stderr.String(), "TypeError: unhandled"), server.stderr)
}
