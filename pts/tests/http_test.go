package tests

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/pts/driver"
	"github.com/microsoft/typescript-go/pts/toolchain"
	"gotest.tools/v3/assert"
)

// dated matches the date header the server adds to a response: a day, a date and a time in GMT.
var dated = regexp.MustCompile(`date: [A-Z][a-z]{2}, \d{2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2} GMT\r\n`)

// httpServer is a running server program from http/: its servers' base URLs, its stdout and its process.
type httpServer struct {
	main, plain string
	lines       *bufio.Scanner
	stderr      *strings.Builder
	cmd         *exec.Cmd
}

// buildServer builds http/<name>.pts and returns the executable's path.
func buildServer(t *testing.T, name string) string {
	t.Helper()
	exe := filepath.Join(buildDir, "http_"+name)
	source, err := filepath.Abs(filepath.Join("http", name+".pts"))
	assert.NilError(t, err)
	heavyJobs <- struct{}{}
	err = driver.Build(context.Background(), source, exe)
	<-heavyJobs
	assert.NilError(t, err)
	return exe
}

// startServer starts a server program with extra environment variables and reads its first line, "listening" and the ports.
func startServer(t *testing.T, exe string, env ...string) (server *httpServer, ports []string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), exe)
	cmd.Env = append(os.Environ(), env...)
	stdout, err := cmd.StdoutPipe()
	assert.NilError(t, err)
	server = &httpServer{cmd: cmd, lines: bufio.NewScanner(stdout), stderr: &strings.Builder{}}
	cmd.Stderr = server.stderr
	assert.NilError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	assert.Assert(t, server.lines.Scan(), "no output: %s", server.stderr)
	fields := strings.Fields(server.lines.Text())
	assert.Assert(t, len(fields) >= 2 && fields[0] == "listening", server.lines.Text())
	return server, fields[1:]
}

// awaitExit waits for a server program that was told to stop to end by itself, and fails if it doesn't within a few seconds.
func awaitExit(t *testing.T, server *httpServer) {
	t.Helper()
	exited := make(chan error, 1)
	go func() { exited <- server.cmd.Wait() }()
	select {
	case err := <-exited:
		assert.NilError(t, err, "stderr:\n%s", server.stderr)
	case <-time.After(10 * time.Second):
		t.Fatalf("the server didn't end after stop(); stderr:\n%s", server.stderr)
	}
}

// startHTTPServer builds and starts http/server.pts and reads the ports it listens on.
func startHTTPServer(t *testing.T) *httpServer {
	t.Helper()
	server, fields := startServer(t, buildServer(t, "server"))
	assert.Equal(t, len(fields), 3, server.lines.Text())
	assert.Equal(t, fields[2], "true", "Server.url")
	server.main, server.plain = "http://127.0.0.1:"+fields[0], "http://127.0.0.1:"+fields[1]
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
	date, err := http.ParseTime(response.Header.Get("Date"))
	assert.NilError(t, err)
	assert.Assert(t, time.Since(date).Abs() < time.Minute, date)
	response, _ = request(t, "GET", server.main+"/dated", "")
	assert.DeepEqual(t, response.Header.Values("Date"), []string{"Tue, 01 Jan 2030 00:00:00 GMT"})
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
	assert.Assert(t, dated.MatchString(malformed), malformed)
	assert.Equal(t, dated.ReplaceAllString(malformed, ""), "HTTP/1.1 400 Bad Request\r\ncontent-length: 0\r\nconnection: close\r\n\r\n")
	bareCR := exchange(t, server.main, "GET /hello HTTP/1.1\r\nHost: a\r\nX-Split: a\rb\r\n\r\n")
	assert.Assert(t, strings.HasPrefix(bareCR, "HTTP/1.1 400 Bad Request\r\n"), bareCR)

	_, body = request(t, "GET", server.main+"/stop", "")
	assert.Equal(t, body, "bye")
	var log []string
	for server.lines.Scan() {
		log = append(log, server.lines.Text())
	}
	awaitExit(t, server)
	assert.DeepEqual(t, log, []string{"GET /hello", "POST /echo", "POST /json?x=1", "GET /status", "HEAD /hello", "GET /dated", "GET /later", "GET /missing", "GET /throw",
		"GET /hello", "POST /echo", "GET /hello", "GET /hello", "POST /echo", "GET /stop"})
	assert.Assert(t, strings.Contains(server.stderr.String(), "TypeError: unhandled"), server.stderr)
}

// getAll sends GET path count times over each of clients concurrent clients and returns the bodies.
func getAll(t *testing.T, client *http.Client, url string, clients int, count int) []string {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	var wg sync.WaitGroup
	for range clients {
		wg.Go(func() {
			for range count {
				response, err := client.Get(url)
				if err != nil {
					t.Error(err)
					return
				}
				text, err := io.ReadAll(response.Body)
				response.Body.Close()
				assert.Check(t, err)
				mu.Lock()
				bodies = append(bodies, string(text))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return bodies
}

// TestHTTPServerEveryLoop runs a server with reusePort (D-137) on four loops and on one:
// on four, handlers on different loops run at the same time; on both, timers and tasks
// work from the loop that took the request, no request is lost, and stop() reaches every
// loop, so the program ends.
func TestHTTPServerEveryLoop(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "every_loop")
	for _, threads := range []int{1, 4} {
		t.Run(fmt.Sprintf("PTS_THREADS=%d", threads), func(t *testing.T) {
			t.Parallel()
			server, ports := startServer(t, exe, fmt.Sprintf("PTS_THREADS=%d", threads))
			base := "http://127.0.0.1:" + ports[0]
			requests := 0
			if threads > 1 {
				fresh := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
				meetings := getAll(t, fresh, base+"/meet", 8, 1)
				assert.Assert(t, slices.Contains(meetings, "together"), "no two handlers ran at once: %v", meetings)
				requests += len(meetings)
			}
			hellos := getAll(t, http.DefaultClient, base+"/", 8, 8)
			assert.DeepEqual(t, hellos, slices.Repeat([]string{"hello"}, 64))
			_, later := request(t, "GET", base+"/later", "")
			assert.Equal(t, later, "later")
			_, fib := request(t, "GET", base+"/fib", "")
			assert.Equal(t, fib, "6765")
			requests += len(hellos) + 2
			_, count := request(t, "GET", base+"/count", "")
			assert.Equal(t, count, fmt.Sprint(requests+1))
			_, body := request(t, "GET", base+"/stop", "")
			assert.Equal(t, body, "bye")
			awaitExit(t, server)
		})
	}
}

// TestHTTPServerSignalsEveryLoop runs a reusePort server (D-137) whose handlers, on every
// loop at once, read their requests' signals and make, follow and abort signals of their own
// (C07, H07): every one of many concurrent requests over fresh connections, which the kernel
// spreads over the loops, must answer "ok", with nothing on stderr, on one loop and on several.
// Handlers' own timeouts fire on their loops, and clients that leave abort their held
// requests' signals (D-163) on whichever loops took them.
func TestHTTPServerSignalsEveryLoop(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "signals_every_loop")
	for _, threads := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("PTS_THREADS=%d", threads), func(t *testing.T) {
			t.Parallel()
			server, ports := startServer(t, exe, fmt.Sprintf("PTS_THREADS=%d", threads))
			base := "http://127.0.0.1:" + ports[0]
			fresh := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 10 * time.Second}
			bodies := getAll(t, fresh, base+"/", 16, 50)
			assert.DeepEqual(t, tallyBodies(bodies), map[string]int{"ok": 16 * 50})
			timeouts := getAll(t, fresh, base+"/timeout", 8, 8)
			assert.DeepEqual(t, tallyBodies(timeouts), map[string]int{"TimeoutError": 8 * 8})
			const holders = 16
			count := func() string {
				_, body := request(t, "GET", base+"/count", "")
				return body
			}
			leaving := make([]*rawConn, holders)
			for i := range leaving {
				leaving[i] = dialRaw(t, strings.TrimPrefix(base, "http://"))
				leaving[i].send(t, "GET /hold HTTP/1.1\r\nHost: a\r\n\r\n")
			}
			eventually(t, 5*time.Second, func() bool { return count() == fmt.Sprintf("%d 0", holders) }, "every held request arrives")
			for _, conn := range leaving {
				_ = conn.Close()
			}
			eventually(t, 5*time.Second, func() bool { return count() == fmt.Sprintf("%d %d", holders, holders) }, "every held request's signal aborts")
			_, body := request(t, "GET", base+"/stop", "")
			assert.Equal(t, body, "bye")
			awaitExit(t, server)
			assert.Equal(t, server.stderr.String(), "")
		})
	}
}

// tallyBodies counts each distinct body, so that a failure shows how many requests got which answer.
func tallyBodies(bodies []string) map[string]int {
	counts := map[string]int{}
	for _, body := range bodies {
		counts[body]++
	}
	return counts
}

// TestFetchFromGoServer runs http/fetch_client.pts against a Go server (D-160, D-162), for
// the responses pts:http doesn't send: chunked and read-until-close bodies, a 1xx before
// the response, a malformed response, connections closed or reset before the body's end,
// bodies too large to take and a large chunked one; and fetches nobody awaits, which keep
// the program running until they are done.
func TestFetchFromGoServer(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "fetch_client")
	hijack := func(w http.ResponseWriter, raw string) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			panic(err)
		}
		_, _ = conn.Write([]byte(raw))
		_ = conn.Close()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		for _, part := range []string{"one ", "two ", "three"} {
			_, _ = io.WriteString(w, part)
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/close", func(w http.ResponseWriter, r *http.Request) {
		hijack(w, "HTTP/1.1 200 OK\r\ncontent-type: text/plain\r\n\r\nread until the end")
	})
	mux.HandleFunc("/early", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("link", "</style.css>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		_, _ = io.WriteString(w, "after the hints")
	})
	mux.HandleFunc("/malformed", func(w http.ResponseWriter, r *http.Request) { hijack(w, "NOT HTTP AT ALL\r\n\r\n") })
	mux.HandleFunc("/truncated", func(w http.ResponseWriter, r *http.Request) {
		hijack(w, "HTTP/1.1 200 OK\r\ncontent-length: 100\r\n\r\nonly this")
	})
	mux.HandleFunc("/truncated-chunks", func(w http.ResponseWriter, r *http.Request) {
		hijack(w, "HTTP/1.1 200 OK\r\ntransfer-encoding: chunked\r\n\r\n5\r\nhello\r\n")
	})
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			panic(err)
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\ncontent-length: 100\r\n\r\npartial"))
		_ = conn.(*net.TCPConn).SetLinger(0) // closing sends a reset
		_ = conn.Close()
	})
	mux.HandleFunc("/huge", func(w http.ResponseWriter, r *http.Request) {
		hijack(w, "HTTP/1.1 200 OK\r\ncontent-length: 100000000\r\n\r\nabc")
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		piece := strings.Repeat("y", 64*1024)
		for range 320 {
			_, _ = io.WriteString(w, piece)
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/headers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Multi", "a")
		w.Header().Add("X-Multi", "b")
		w.Header().Add("Set-Cookie", "c=1")
		w.Header().Add("Set-Cookie", "d=2")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<p>")
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/chunked", http.StatusFound) })
	mux.HandleFunc("/teapot", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "short and stout")
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var names []string
		for name := range r.Header {
			names = append(names, strings.ToLower(name)+"="+strings.Join(r.Header.Values(name), ","))
		}
		slices.Sort(names)
		fmt.Fprintf(w, "%s %s %s close=%v length=%d %s %q", r.Method, r.RequestURI, r.Host[:strings.Index(r.Host, ":")], r.Close, r.ContentLength, strings.Join(names, " "), body)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	go func() { _ = http.Serve(listener, mux) }()
	t.Cleanup(func() { _ = listener.Close() })

	base := "http://" + listener.Addr().String()
	out, err := exec.CommandContext(t.Context(), exe, base).CombinedOutput()
	assert.NilError(t, err, "%s", out)
	assert.Equal(t, string(out), `chunked 200 one two three
until close 200 read until the end
early hints 200 after the hints
malformed TypeError: fetch failed | Response does not match the HTTP/1.1 protocol
truncated TypeError: fetch failed | other side closed
truncated chunks TypeError: fetch failed | other side closed
headers a, b | c=1 d=2 | text/html; charset=utf-8
redirect 200 true /chunked one two three
status 418 I'm a teapot false short and stout
no content 204 ""
request POST /echo?q=1%202 127.0.0.1 close=true length=8 accept=*/* connection=close content-length=8 content-type=text/plain;charset=UTF-8 user-agent=ParallelScript x-test=a, b "body ✓"
reset TypeError: fetch failed | read ECONNRESET
declared too large TypeError: fetch failed | Response body is larger than 64 MB
head of too large 200 100000000
big 20971520
not awaited 200 one two three
not awaited, on another loop read until the end
`)
}

// buildSanitized builds the program at source with AddressSanitizer and
// UndefinedBehaviorSanitizer (toolchain.Sanitize) and returns the executable's path.
func buildSanitized(t *testing.T, source string) string {
	t.Helper()
	heavyJobs <- struct{}{}
	defer func() { <-heavyJobs }()
	c, err := driver.EmitC(t.Context(), source)
	assert.NilError(t, err)
	tc, err := toolchain.New()
	assert.NilError(t, err)
	rt, err := tc.Runtime(t.Context(), toolchain.Sanitize)
	assert.NilError(t, err)
	cFile := filepath.Join(t.TempDir(), "main.c")
	assert.NilError(t, os.WriteFile(cFile, []byte(c), 0o644))
	exe := filepath.Join(buildDir, "sanitized_"+strings.TrimSuffix(filepath.Base(source), ".pts"))
	assert.NilError(t, tc.CompileProgram(t.Context(), rt, []string{cFile}, exe))
	return exe
}

// sanitizerNoise matches the line of run/fetch_many.pts that measures how much the process
// grew, which AddressSanitizer's quarantine of freed memory makes meaningless.
var sanitizerNoise = regexp.MustCompile(`(?m)^grew less than 40 MB: .*\n`)

// TestFetchUnderSanitizers runs the fetch programs that abort requests at every stage
// (D-162) built with AddressSanitizer and UndefinedBehaviorSanitizer, on one loop and on
// four, so that a client freed too early or twice, or a handle used after it closed, fails.
func TestFetchUnderSanitizers(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"fetch_abort", "fetch_many"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source, err := filepath.Abs(filepath.Join("run", name+".pts"))
			assert.NilError(t, err)
			spec, err := readSpec(source)
			assert.NilError(t, err)
			exe := buildSanitized(t, source)
			for _, threads := range []string{"1", "4"} {
				cmd := exec.CommandContext(t.Context(), exe)
				// As the runtime's tests under sanitizers: the loops live as long as the process, and the collector scans real stacks.
				cmd.Env = append(os.Environ(), "PTS_THREADS="+threads, "TZ=America/New_York", "ASAN_OPTIONS=detect_leaks=0:detect_stack_use_after_return=0")
				out, err := cmd.CombinedOutput()
				assert.NilError(t, err, "PTS_THREADS=%s:\n%s", threads, out)
				assert.Equal(t, sanitizerNoise.ReplaceAllString(string(out), ""), sanitizerNoise.ReplaceAllString(*spec.stdout, ""), "PTS_THREADS=%s", threads)
			}
		})
	}
}

// openFiles is how many files process pid has open.
func openFiles(t *testing.T, pid int) int {
	t.Helper()
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	assert.NilError(t, err)
	return len(entries)
}

// residentMB is how much memory process pid has resident, in MB.
func residentMB(t *testing.T, pid int) float64 {
	t.Helper()
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	assert.NilError(t, err)
	m := regexp.MustCompile(`VmRSS:\s+(\d+) kB`).FindSubmatch(status)
	assert.Assert(t, m != nil, "no VmRSS in /proc/%d/status", pid)
	var kb float64
	_, err = fmt.Sscan(string(m[1]), &kb)
	assert.NilError(t, err)
	return kb / 1024
}

// eventually fails unless done reports true within d.
func eventually(t *testing.T, d time.Duration, done func() bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(d); !done(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("not within %v: %s", d, what)
		}
	}
}

// rawConn is a TCP connection to address that writes raw requests and reads responses.
type rawConn struct {
	net.Conn
	reader *bufio.Reader
}

func dialRaw(t *testing.T, address string) *rawConn {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &rawConn{conn, bufio.NewReader(conn)}
}

func (c *rawConn) send(t *testing.T, raw string) {
	t.Helper()
	_, err := c.Write([]byte(raw))
	assert.NilError(t, err)
}

// response reads the next response and its body.
func (c *rawConn) response(t *testing.T) (*http.Response, string) {
	t.Helper()
	assert.NilError(t, c.SetReadDeadline(time.Now().Add(20*time.Second)))
	response, err := http.ReadResponse(c.reader, nil)
	assert.NilError(t, err)
	body, err := io.ReadAll(response.Body)
	assert.NilError(t, err)
	return response, string(body)
}

// closedByServer reports whether the server closed the connection without sending anything more.
func (c *rawConn) closedByServer(t *testing.T) bool {
	t.Helper()
	assert.NilError(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
	n, err := c.reader.Read(make([]byte, 1))
	return n == 0 && err == io.EOF
}

// TestHTTPServerLifecycle runs http/lifecycle.pts (D-163) on one loop and on four: a client
// that leaves while its request is held aborts the request's signal, and its connection
// closes at once even if the handler ignores the signal; 10,000 pipelined requests are
// answered in order; a client that doesn't read keeps the server from queueing more than
// about a megabyte of responses; stop() from another loop lets a request in flight finish
// (with connection: close), closes idle connections and refuses new ones; stop(true) from
// a pool thread closes active connections, aborting their requests' signals; then the
// program ends. It runs a third time built with AddressSanitizer and
// UndefinedBehaviorSanitizer, where only the memory it grows by isn't checked.
func TestHTTPServerLifecycle(t *testing.T) {
	t.Parallel()
	exe := buildServer(t, "lifecycle")
	source, err := filepath.Abs(filepath.Join("http", "lifecycle.pts"))
	assert.NilError(t, err)
	sanitized := buildSanitized(t, source)
	for _, run := range []struct {
		threads   int
		sanitized bool
	}{{1, false}, {4, false}, {4, true}} {
		t.Run(fmt.Sprintf("PTS_THREADS=%d sanitized=%v", run.threads, run.sanitized), func(t *testing.T) {
			t.Parallel()
			env := []string{fmt.Sprintf("PTS_THREADS=%d", run.threads)}
			program := exe
			if run.sanitized {
				program = sanitized
				env = append(env, "ASAN_OPTIONS=detect_leaks=0:detect_stack_use_after_return=0")
			}
			server, ports := startServer(t, program, env...)
			main, second := "127.0.0.1:"+ports[0], "127.0.0.1:"+ports[1]
			pid := server.cmd.Process.Pid
			expectLine := func(want string) {
				t.Helper()
				assert.Assert(t, server.lines.Scan(), "no line %q; stderr:\n%s", want, server.stderr)
				assert.Equal(t, server.lines.Text(), want)
			}
			files := openFiles(t, pid)
			sameFiles := func() bool { return openFiles(t, pid) == files }

			held := dialRaw(t, main)
			held.send(t, "GET /hold HTTP/1.1\r\nHost: a\r\n\r\n")
			expectLine("holding")
			_ = held.Close()
			expectLine("client left: AbortError")
			eventually(t, 2*time.Second, sameFiles, "the held request's connection closes")

			ignored := dialRaw(t, main)
			ignored.send(t, "GET /ignore HTTP/1.1\r\nHost: a\r\n\r\n")
			expectLine("ignoring")
			_ = ignored.Close()
			eventually(t, time.Second, sameFiles, "the connection closes before the handler answers")
			expectLine("answered late: true")

			pipelined := dialRaw(t, main)
			go func() { _, _ = pipelined.Write([]byte(strings.Repeat("GET /tiny HTTP/1.1\r\nHost: a\r\n\r\n", 10000))) }()
			for i := range 10000 {
				response, body := pipelined.response(t)
				if response.StatusCode != 200 || body != "t" {
					t.Fatalf("pipelined response %d: %d %q", i, response.StatusCode, body)
				}
			}

			resident := residentMB(t, pid)
			slow := dialRaw(t, main)
			slow.send(t, strings.Repeat("GET /big HTTP/1.1\r\nHost: a\r\n\r\n", 64))
			time.Sleep(500 * time.Millisecond)
			grown := residentMB(t, pid) - resident
			assert.Assert(t, grown < 24 || run.sanitized, "64 unread 1 MB responses grew the server by %.1f MB", grown) // AddressSanitizer keeps freed memory
			for i := range 64 {
				_, body := slow.response(t)
				assert.Equal(t, len(body), 1024*1024, "response %d", i)
			}

			inFlight := dialRaw(t, main)
			inFlight.send(t, "GET /slow HTTP/1.1\r\nHost: a\r\n\r\n")
			expectLine("slow")
			idle := dialRaw(t, main)
			idle.send(t, "GET /tiny HTTP/1.1\r\nHost: a\r\n\r\n")
			_, body := idle.response(t)
			assert.Equal(t, body, "t")
			stopper := dialRaw(t, main)
			stopper.send(t, "GET /stop HTTP/1.1\r\nHost: a\r\n\r\n")
			expectLine("stopped")
			response, body := stopper.response(t)
			assert.Equal(t, body, "stopping")
			assert.Assert(t, response.Close, "the answer after stop() closes its connection")
			response, body = inFlight.response(t)
			assert.Equal(t, body, "slow done")
			assert.Assert(t, response.Close, "a request in flight is answered, then its connection closes")
			assert.Assert(t, idle.closedByServer(t), "stop() closes idle connections")
			_, err := net.Dial("tcp", main)
			assert.ErrorContains(t, err, "connection refused")

			active := dialRaw(t, second)
			active.send(t, "GET /hold HTTP/1.1\r\nHost: a\r\n\r\n")
			expectLine("holding on the second server")
			stopNow := dialRaw(t, second)
			stopNow.send(t, "GET /stop HTTP/1.1\r\nHost: a\r\n\r\n")
			_, body = stopNow.response(t)
			assert.Equal(t, body, "stopping now")
			expectLine("closed by stop(true): AbortError")
			assert.Assert(t, active.closedByServer(t), "stop(true) closes a connection whose request is in flight")
			awaitExit(t, server)
		})
	}
}
