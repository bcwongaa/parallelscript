package tests

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"maps"
	"math/big"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/pts/driver"
	"gotest.tools/v3/assert"
)

// buildExample builds examples/<name>/main.pts and returns the executable's path.
func buildExample(t *testing.T, name string) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", "..", "examples", name, "main.pts"))
	assert.NilError(t, err)
	exe := filepath.Join(buildDir, "example_"+name)
	heavyJobs <- struct{}{}
	err = driver.Build(context.Background(), source, exe)
	<-heavyJobs
	assert.NilError(t, err)
	return exe
}

// TestExampleHasher runs examples/hasher (D-165) on files whose sizes cover SHA-256's
// padding cases, a few megabytes of random bytes and a nested directory, under one thread
// and four, and compares its lines with Go's digests; a file that doesn't exist is
// reported on stderr with exit code 1.
func TestExampleHasher(t *testing.T) {
	t.Parallel()
	exe := buildExample(t, "hasher")
	dir := t.TempDir()
	random := rand.New(rand.NewPCG(1, 2))
	sizes := map[string]int{"empty": 0, "one": 1, "b55": 55, "b56": 56, "b63": 63, "b64": 64, "b65": 65, "b119": 119, "b120": 120, "nested/deeper/big": 3_000_001}
	var want []string
	for name, size := range sizes {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(random.Uint32())
		}
		path := filepath.Join(dir, name)
		assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		assert.NilError(t, os.WriteFile(path, data, 0o644))
		digest := sha256.Sum256(data)
		want = append(want, hex.EncodeToString(digest[:])+"  "+path)
	}
	sortLinesByPath(want)
	for _, threads := range []string{"1", "4"} {
		cmd := exec.CommandContext(t.Context(), exe, dir)
		cmd.Env = append(os.Environ(), "PTS_THREADS="+threads)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		assert.NilError(t, err, "stderr: %s", stderr.String())
		assert.DeepEqual(t, strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"), want)
		assert.Assert(t, strings.HasPrefix(stderr.String(), fmt.Sprintf("hashed %d files, 3.0 MB in ", len(sizes))), stderr.String())
	}

	missing := filepath.Join(dir, "missing")
	cmd := exec.CommandContext(t.Context(), exe, filepath.Join(dir, "one"), missing)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	assert.ErrorContains(t, err, "exit status 1")
	assert.Assert(t, strings.HasSuffix(string(out), "  "+filepath.Join(dir, "one")+"\n"), string(out))
	assert.Assert(t, strings.Contains(stderr.String(), "hasher: ENOENT: no such file or directory, open '"+missing+"'"), stderr.String())
}

// sortLinesByPath sorts "<digest>  <path>" lines by path, as the hasher orders a directory's files.
func sortLinesByPath(lines []string) {
	pathOf := func(line string) string { return line[strings.Index(line, "  ")+2:] }
	slices.SortFunc(lines, func(a, b string) int { return strings.Compare(pathOf(a), pathOf(b)) })
}

// TestExampleMandelbrot runs examples/mandelbrot (D-164) under one thread and four: the
// files are the same, Go's image/png decodes them (so the stored-deflate stream, the CRCs
// and Adler-32 are right), and a point of the main cardioid is black where a corner isn't.
func TestExampleMandelbrot(t *testing.T) {
	t.Parallel()
	exe := buildExample(t, "mandelbrot")
	dir := t.TempDir()
	var files [][]byte
	for _, threads := range []string{"1", "4"} {
		path := filepath.Join(dir, "set"+threads+".png")
		cmd := exec.CommandContext(t.Context(), exe, path, "240", "160")
		cmd.Env = append(os.Environ(), "PTS_THREADS="+threads)
		out, err := cmd.CombinedOutput()
		assert.NilError(t, err, "%s", out)
		assert.Assert(t, strings.HasPrefix(string(out), path+": 240×160, rendered in "), string(out))
		file, err := os.ReadFile(path)
		assert.NilError(t, err)
		files = append(files, file)
	}
	assert.Assert(t, bytes.Equal(files[0], files[1]), "the image depends on the number of threads")
	image, err := png.Decode(bytes.NewReader(files[0]))
	assert.NilError(t, err)
	assert.Equal(t, image.Bounds().Dx(), 240)
	assert.Equal(t, image.Bounds().Dy(), 160)
	black := func(x, y int) bool {
		r, g, b, _ := image.At(x, y).RGBA()
		return r == 0 && g == 0 && b == 0
	}
	assert.Assert(t, black(154, 80), "(-0.25, 0), in the main cardioid, isn't black")
	assert.Assert(t, !black(0, 0), "a corner, far outside the set, is black")
	colors := map[[3]uint32]bool{}
	for y := 0; y < 160; y += 4 {
		for x := 0; x < 240; x += 4 {
			r, g, b, _ := image.At(x, y).RGBA()
			colors[[3]uint32{r, g, b}] = true
		}
	}
	assert.Assert(t, len(colors) > 50, "only %d colors", len(colors))
}

// apiItem is an item of examples/api-server as its JSON says.
type apiItem struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

// apiClient sends requests to a running examples/api-server and counts them.
type apiClient struct {
	t    *testing.T
	base string
	sent atomic.Int64
}

// call sends a request and returns the response's status and body.
func (c *apiClient) call(method string, path string, body string) (int, string) {
	c.t.Helper()
	c.sent.Add(1)
	response, text := request(c.t, method, c.base+path, body)
	return response.StatusCode, text
}

// stats reads GET /stats.
func (c *apiClient) stats() (requests int64, items int, abandoned int) {
	c.t.Helper()
	status, body := c.call("GET", "/stats", "")
	assert.Equal(c.t, status, 200, body)
	var stats struct {
		Requests  int64 `json:"requests"`
		Items     int   `json:"items"`
		Abandoned int   `json:"abandoned"`
	}
	assert.NilError(c.t, json.Unmarshal([]byte(body), &stats), body)
	return stats.Requests, stats.Items, stats.Abandoned
}

// startAPIServer starts examples/api-server on a free port with PTS_THREADS=threads.
func startAPIServer(t *testing.T, exe string, threads string) (*httpServer, *apiClient) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), exe, "0")
	cmd.Env = append(os.Environ(), "PTS_THREADS="+threads)
	stdout, err := cmd.StdoutPipe()
	assert.NilError(t, err)
	server := &httpServer{cmd: cmd, lines: bufio.NewScanner(stdout), stderr: &strings.Builder{}}
	cmd.Stderr = server.stderr
	assert.NilError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	assert.Assert(t, server.lines.Scan(), "no output: %s", server.stderr)
	base, found := strings.CutPrefix(server.lines.Text(), "api-server listening on ")
	assert.Assert(t, found, server.lines.Text())
	return server, &apiClient{t: t, base: strings.TrimSuffix(base, "/")}
}

// TestExampleAPIServer drives examples/api-server over HTTP on one loop and on four:
// items added by concurrent clients all get ids of their own and are all listed; reads,
// deletes and the 400, 404 and 405 errors; a prime count; a slow request whose client
// gives up is counted as abandoned (request.signal); every request is counted; and
// POST /shutdown ends the program.
func TestExampleAPIServer(t *testing.T) {
	t.Parallel()
	exe := buildExample(t, "api-server")
	for _, threads := range []string{"1", "4"} {
		t.Run("PTS_THREADS="+threads, func(t *testing.T) {
			t.Parallel()
			server, api := startAPIServer(t, exe, threads)
			status, body := api.call("GET", "/items", "")
			assert.Equal(t, status, 200)
			assert.Equal(t, body, "[]")

			const clients, perClient = 8, 25
			added := make(chan apiItem, clients*perClient)
			var wg sync.WaitGroup
			for client := range clients {
				wg.Go(func() {
					for i := range perClient {
						status, body := api.call("POST", "/items", fmt.Sprintf(`{"name":"item %d-%d","price":%d.5}`, client, i, i))
						var item apiItem
						if assert.Check(t, status == 201, body) && assert.Check(t, json.Unmarshal([]byte(body), &item)) {
							added <- item
						}
					}
				})
			}
			wg.Wait()
			close(added)
			want := map[int]apiItem{}
			for item := range added {
				_, taken := want[item.ID]
				assert.Assert(t, !taken, "id %d given twice", item.ID)
				want[item.ID] = item
			}
			assert.Equal(t, len(want), clients*perClient)
			var listed []apiItem
			_, body = api.call("GET", "/items", "")
			assert.NilError(t, json.Unmarshal([]byte(body), &listed))
			got := map[int]apiItem{}
			for _, item := range listed {
				got[item.ID] = item
			}
			assert.DeepEqual(t, got, want)

			first := want[1]
			status, body = api.call("GET", "/items/1", "")
			assert.Equal(t, status, 200)
			assert.Equal(t, body, fmt.Sprintf(`{"id":1,"name":%q,"price":%v}`, first.Name, first.Price))
			status, _ = api.call("DELETE", "/items/1", "")
			assert.Equal(t, status, 204)
			for _, call := range []struct{ method, path, body, want string }{
				{"GET", "/items/1", "", `404 {"error":"no item 1"}`},
				{"DELETE", "/items/1", "", `404 {"error":"no item 1"}`},
				{"GET", "/items/abc", "", `404 {"error":"no item abc"}`},
				{"POST", "/items", "{oops", `400 {"error":"the body isn't JSON: Expected property name or '}' in JSON at position 1 (line 1 column 2)"}`},
				{"POST", "/items", `{"price":1}`, `400 {"error":"name must be a string that isn't empty"}`},
				{"POST", "/items", `{"name":"x","price":-1}`, `400 {"error":"price must be a number that isn't negative"}`},
				{"PUT", "/items", "", `405 {"error":"PUT isn't allowed on /items"}`},
				{"GET", "/nowhere", "", `404 {"error":"no route /nowhere"}`},
				{"GET", "/primes?below=100000", "", `200 {"below":100000,"primes":9592,"ms":`},
				{"GET", "/primes?below=ten", "", `400 {"error":"below must be a whole number from 0 to 50000000"}`},
				{"GET", "/slow?ms=20", "", `200 {"waited":20}`},
			} {
				status, body := api.call(call.method, call.path, call.body)
				assert.Assert(t, strings.HasPrefix(fmt.Sprint(status, " ", body), call.want), "%s %s: %d %s", call.method, call.path, status, body)
			}

			// A client that gives up on a slow request: its signal aborts the wait.
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			slow, err := http.NewRequestWithContext(ctx, "GET", api.base+"/slow?ms=20000", nil)
			assert.NilError(t, err)
			api.sent.Add(1)
			_, err = http.DefaultClient.Do(slow)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			deadline := time.Now().Add(5 * time.Second)
			for {
				_, _, abandoned := api.stats()
				if abandoned == 1 {
					break
				}
				assert.Assert(t, time.Now().Before(deadline), "the slow request wasn't abandoned")
				time.Sleep(20 * time.Millisecond)
			}

			requests, items, _ := api.stats()
			assert.Equal(t, requests, api.sent.Load())
			assert.Equal(t, items, clients*perClient-1)
			status, body = api.call("POST", "/shutdown", "")
			assert.Equal(t, fmt.Sprint(status, " ", body), `200 {"stopping":true}`)
			awaitExit(t, server)
		})
	}
}

// accessLog holds what generated access logs say, as examples/log-analyzer reports it.
type accessLog struct {
	lines, malformed, requests, bytes int
	statuses                          map[int]int
	paths                             map[string]int
	hours                             map[string][2]int // "2026-10-05 13:00": requests and bytes
	clients                           map[string]bool
}

var (
	logMonths = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	logPaths  = []string{"/", "/index.html", "/about", "/api/items", "/api/items?page=2", "/static/app.js", "/static/style.css",
		"/favicon.ico", "/login", "/search?q=parallel+script", "/blog/1", "/blog/2", "/blog/3", "/feed.xml", "/robots.txt"}
	logMalformed = []string{
		"not a log line",
		`10.0.0.1 - - [05/Oct/2026:13:55:36 +0000] "GET / HTTP/1.1"`, // cut before the status
		`10.0.0.1 - - [05/Foo/2026:13:55:36 +0000] "GET / HTTP/1.1" 200 512`,
		`10.0.0.1 - - [05/Oct/2026:13:55:36 +0000] "GET / HTTP/1.1" 2000 512`,
		`10.0.0.1 - - [05/Oct/2026:13:55:36 +0000] "get / HTTP/1.1" 200 512`,
		"ünïcödé, but no log line",
	}
)

// write writes count generated lines to path and adds them to the log: about one in fifty
// malformed, the others in the Common or the Combined Log Format, a few empty lines, line
// ends of CRLF if crlf, and no line end after the last line if unterminated.
func (log *accessLog) write(t *testing.T, path string, random *rand.Rand, count int, crlf bool, unterminated bool) {
	t.Helper()
	end := "\n"
	if crlf {
		end = "\r\n"
	}
	var text strings.Builder
	for range count {
		if random.IntN(300) == 0 {
			text.WriteString(end)
		}
		log.lines++
		if random.IntN(50) == 0 {
			text.WriteString(logMalformed[random.IntN(len(logMalformed))] + end)
			log.malformed++
			continue
		}
		client := fmt.Sprintf("192.168.%d.%d", random.IntN(4), random.IntN(256))
		day, month, hour := 1+random.IntN(3), 9+random.IntN(2), random.IntN(24)
		status := []int{200, 200, 200, 200, 200, 200, 301, 304, 404, 500}[random.IntN(10)]
		sent, bytes := "-", 0
		if status != 304 {
			bytes = random.IntN(100_000)
			sent = strconv.Itoa(bytes)
		}
		path := logPaths[min(int(random.ExpFloat64()*4), len(logPaths)-1)]
		line := fmt.Sprintf(`%s - %s [%02d/%s/2026:%02d:%02d:%02d %s] "%s %s HTTP/1.1" %d %s`,
			client, []string{"-", "frank"}[random.IntN(2)], day, logMonths[month], hour, random.IntN(60), random.IntN(60),
			[]string{"+0000", "-0700"}[random.IntN(2)], []string{"GET", "POST", "HEAD"}[random.IntN(3)], path, status, sent)
		if random.IntN(2) == 0 {
			line += ` "https://example.com/" "Mozilla/5.0 (X11; Linux x86_64)"`
		}
		text.WriteString(line + end)
		log.requests++
		log.bytes += bytes
		log.clients[client] = true
		log.statuses[status]++
		log.paths[path]++
		key := fmt.Sprintf("2026-%02d-%02d %02d:00", month+1, day, hour)
		log.hours[key] = [2]int{log.hours[key][0] + 1, log.hours[key][1] + bytes}
	}
	data := text.String()
	if unterminated {
		data = strings.TrimSuffix(data, end)
	}
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte(data), 0o644))
}

// report is the log-analyzer's report of the log's files.
func (log *accessLog) report(files int) string {
	var b strings.Builder
	plural := "s"
	if files == 1 {
		plural = ""
	}
	fmt.Fprintf(&b, "%d file%s, %s lines (%s malformed)\n", files, plural, thousands(log.lines), thousands(log.malformed))
	fmt.Fprintf(&b, "%s requests from %s clients, %s sent\n", thousands(log.requests), thousands(len(log.clients)), megabytes(log.bytes))

	b.WriteString("\nstatus    requests   share\n")
	for _, status := range slices.Sorted(maps.Keys(log.statuses)) {
		n := log.statuses[status]
		fmt.Fprintf(&b, "%-6d  %10s  %6s\n", status, thousands(n), toFixed1(float64(100*n)/float64(log.requests))+"%")
	}

	paths := slices.SortedFunc(maps.Keys(log.paths), func(a, b string) int {
		return cmp.Or(log.paths[b]-log.paths[a], strings.Compare(a, b))
	})[:10]
	width := 4
	for _, path := range paths {
		width = max(width, len(path))
	}
	fmt.Fprintf(&b, "\n%-*s    requests\n", width, "path")
	for _, path := range paths {
		fmt.Fprintf(&b, "%-*s  %10s\n", width, path, thousands(log.paths[path]))
	}

	b.WriteString("\nhour                requests        sent\n")
	for _, hour := range slices.Sorted(maps.Keys(log.hours)) {
		fmt.Fprintf(&b, "%s  %10s  %10s\n", hour, thousands(log.hours[hour][0]), megabytes(log.hours[hour][1]))
	}
	return b.String()
}

// thousands writes n with commas between groups of three digits, as toLocaleString does.
func thousands(n int) string {
	digits := strconv.Itoa(n)
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}

func megabytes(bytes int) string {
	return toFixed1(float64(bytes)/1e6) + " MB"
}

// toFixed1 is JS's x.toFixed(1) for x >= 0: the exact value rounded to tenths, halves up.
func toFixed1(x float64) string {
	tenths := new(big.Rat).Mul(new(big.Rat).SetFloat64(x), big.NewRat(10, 1))
	tenths.Add(tenths, big.NewRat(1, 2))
	n := new(big.Int).Quo(tenths.Num(), tenths.Denom()).Int64()
	return fmt.Sprintf("%d.%d", n/10, n%10)
}

// TestExampleLogAnalyzer runs examples/log-analyzer under one thread and four on
// generated logs (a directory with a file big enough to be cut into several pieces, one
// with CRLF line ends and no line end at the last line, one with a non-ASCII line) and
// compares its report with the counts the generator kept; a file that doesn't exist is
// reported on stderr with exit code 1.
func TestExampleLogAnalyzer(t *testing.T) {
	t.Parallel()
	exe := buildExample(t, "log-analyzer")
	dir := t.TempDir()
	random := rand.New(rand.NewPCG(3, 4))
	log := &accessLog{statuses: map[int]int{}, paths: map[string]int{}, hours: map[string][2]int{}, clients: map[string]bool{}}
	log.write(t, filepath.Join(dir, "big.log"), random, 30_000, false, false)
	log.write(t, filepath.Join(dir, "old", "crlf.log"), random, 2_000, true, true)
	log.write(t, filepath.Join(dir, "old", "small.log"), random, 500, false, false)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "old", "unicode.log"), []byte("ünïcödé, but no log line\n"), 0o644))
	log.lines++
	log.malformed++
	want := log.report(4)
	for _, threads := range []string{"1", "4"} {
		cmd := exec.CommandContext(t.Context(), exe, dir)
		cmd.Env = append(os.Environ(), "PTS_THREADS="+threads)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		assert.NilError(t, err, "stderr: %s", stderr.String())
		assert.Equal(t, string(out), want)
		assert.Assert(t, strings.HasPrefix(stderr.String(), fmt.Sprintf("analyzed %d lines in ", log.lines)), stderr.String())
	}

	missing := filepath.Join(dir, "missing.log")
	cmd := exec.CommandContext(t.Context(), exe, filepath.Join(dir, "big.log"), missing)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	assert.ErrorContains(t, err, "exit status 1")
	assert.Equal(t, string(out), "")
	assert.Equal(t, stderr.String(), "log-analyzer: ENOENT: no such file or directory, open '"+missing+"'\n")
}
