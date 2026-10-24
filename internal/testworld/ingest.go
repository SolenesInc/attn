package testworld

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
)

const ClickHouseURLEnv = "ATTN_TEST_CLICKHOUSE_URL"

type ClickHouse struct {
	URL string
}

func NewClickHouse(t testing.TB) ClickHouse {
	t.Helper()
	server := os.Getenv(ClickHouseURLEnv)
	if server == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("CI runs the ClickHouse tests: set %s to the ClickHouse service's HTTP URL", ClickHouseURLEnv)
		}
		t.Skipf("set %s to a ClickHouse HTTP URL to run this test; `make ingest-clickhouse` starts one and prints it", ClickHouseURLEnv)
	}
	database := "attn_test_" + randomHex(t, 8)
	clickHouseQuery(t, server, "CREATE DATABASE "+database)
	t.Cleanup(func() { clickHouseQuery(t, server, "DROP DATABASE IF EXISTS "+database) })
	target, err := url.Parse(server)
	if err != nil {
		t.Fatalf("%s: %v", ClickHouseURLEnv, err)
	}
	values := target.Query()
	values.Set("database", database)
	target.RawQuery = values.Encode()
	return ClickHouse{URL: target.String()}
}

func (c ClickHouse) Query(t testing.TB, query string) string {
	t.Helper()
	return clickHouseQuery(t, c.URL, query)
}

func clickHouseQuery(t testing.TB, server, query string) string {
	t.Helper()
	resp, err := oneShotHTTP.Post(server, "text/plain", strings.NewReader(query))
	if err != nil {
		t.Fatalf("clickhouse %q: %v", query, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clickhouse %q: %s: %s", query, resp.Status, body)
	}
	return strings.TrimSpace(string(body))
}

var oneShotHTTP = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

type Ingest struct {
	URL string
}

func (i Ingest) Metric(t testing.TB, name string) int64 {
	t.Helper()
	resp, err := oneShotHTTP.Get(i.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, line := range strings.Split(string(body), "\n") {
		if value, ok := strings.CutPrefix(line, name+" "); ok {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				t.Fatalf("%s: %v", line, err)
			}
			return n
		}
	}
	t.Fatalf("/metrics has no %s:\n%s", name, body)
	return 0
}

var ingestBinary = sync.OnceValues(func() (string, error) {
	if processDir == "" {
		return "", fmt.Errorf("testworld.StartIngest builds into the process directory that testworld.Main creates; call it from TestMain")
	}
	binary := filepath.Join(processDir, "bin", "attn-ingest")
	build := exec.Command("go", "build", "-o", binary, "github.com/victorarias/attn/cmd/attn-ingest")
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build attn-ingest: %w\n%s", err, output)
	}
	return binary, nil
})

func StartIngest(t testing.TB, clickHouse ClickHouse, env ...string) Ingest {
	t.Helper()
	binary, err := ingestBinary()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(),
		"ATTN_INGEST_CLICKHOUSE_URL="+clickHouse.URL,
		"ATTN_INGEST_LISTEN=127.0.0.1:0",
		"ATTN_INGEST_IP_SECRET="+randomHex(t, 32),
	)
	cmd.Env = append(cmd.Env, env...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start attn-ingest: %v", err)
	}
	var log bytes.Buffer
	lines := bufio.NewScanner(stderr)
	address := ""
	for address == "" && lines.Scan() {
		line := lines.Text()
		log.WriteString(line + "\n")
		address, _ = strings.CutPrefix(line, "attn-ingest listening on ")
	}
	if address == "" {
		_ = cmd.Wait()
		t.Fatalf("attn-ingest exited before listening:\n%s", log.String())
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for lines.Scan() {
			log.WriteString(lines.Text() + "\n")
		}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-drained
		if err := cmd.Wait(); err != nil || t.Failed() {
			t.Logf("attn-ingest (%v):\n%s", err, log.String())
		}
	})
	return Ingest{URL: "http://" + address}
}

func randomHex(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

type IngestLink struct {
	URL      string
	upstream *url.URL
	mu       sync.Mutex
	faults   []http.HandlerFunc
	requests []string
	cut      bool
}

func (i Ingest) Link(t testing.TB) *IngestLink {
	t.Helper()
	upstream, err := url.Parse(i.URL)
	if err != nil {
		t.Fatal(err)
	}
	return startLink(t, upstream)
}

func UnreachableIngestLink(t testing.TB) *IngestLink {
	t.Helper()
	return startLink(t, nil)
}

func startLink(t testing.TB, upstream *url.URL) *IngestLink {
	t.Helper()
	link := &IngestLink{upstream: upstream}
	server := httptest.NewServer(http.HandlerFunc(link.serve))
	t.Cleanup(server.Close)
	link.URL = server.URL
	return link
}

func (l *IngestLink) DropNextEventsReply() {
	l.injectEventsFault(func(_ http.ResponseWriter, r *http.Request) {
		l.forward(httptest.NewRecorder(), r)
		panic(http.ErrAbortHandler)
	})
}

func (l *IngestLink) RefuseNextEvents(status int, body string) {
	l.injectEventsFault(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

func (l *IngestLink) Cut() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cut = true
}

func (l *IngestLink) Restore() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cut = false
}

func (l *IngestLink) Requests() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.requests...)
}

func (l *IngestLink) injectEventsFault(fault http.HandlerFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.faults = append(l.faults, fault)
}

func (l *IngestLink) serve(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	l.requests = append(l.requests, r.Method+" "+r.URL.Path)
	handle := l.forward
	if l.cut {
		handle = abort
	} else if r.URL.Path == "/v1/events" && len(l.faults) > 0 {
		handle, l.faults = l.faults[0], l.faults[1:]
	}
	l.mu.Unlock()
	handle(w, r)
}

func abort(http.ResponseWriter, *http.Request) {
	panic(http.ErrAbortHandler)
}

func (l *IngestLink) forward(w http.ResponseWriter, r *http.Request) {
	if l.upstream == nil {
		abort(w, r)
	}
	httputil.NewSingleHostReverseProxy(l.upstream).ServeHTTP(w, r)
}
