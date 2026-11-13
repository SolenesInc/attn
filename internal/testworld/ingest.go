package testworld

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"github.com/victorarias/attn/internal/analytics"
)

const (
	ClickHouseURLEnv      = "ATTN_TEST_CLICKHOUSE_URL"
	ClickHouseRequiredEnv = "ATTN_TEST_REQUIRE_CLICKHOUSE"
)

type ClickHouse struct {
	URL      string
	server   string
	database string
}

func NewClickHouse(t testing.TB) ClickHouse {
	t.Helper()
	server := os.Getenv(ClickHouseURLEnv)
	if server == "" {
		started, err := onDemandClickHouse()
		var unreachable dockerUnreachable
		switch {
		case errors.As(err, &unreachable) && os.Getenv("CI") == "" && os.Getenv(ClickHouseRequiredEnv) == "":
			t.Skipf("%v, or set %s to a ClickHouse HTTP URL", err, ClickHouseURLEnv)
		case err != nil:
			t.Fatalf("%v, or set %s to a ClickHouse HTTP URL", err, ClickHouseURLEnv)
		}
		server = started
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
	return ClickHouse{URL: target.String(), server: server, database: database}
}

func (c ClickHouse) Query(t testing.TB, query string) string {
	t.Helper()
	return clickHouseQuery(t, c.URL, query)
}

func (c ClickHouse) Refuses(t testing.TB, query string) bool {
	t.Helper()
	status, _ := clickHouseAnswer(t, c.URL, query)
	return status != http.StatusOK
}

func (c ClickHouse) User(t testing.TB, grants ...string) ClickHouse {
	t.Helper()
	name, password := "attn_test_user_"+randomHex(t, 8), randomHex(t, 16)
	clickHouseQuery(t, c.server, fmt.Sprintf("CREATE USER %s IDENTIFIED BY '%s'", name, password))
	t.Cleanup(func() {
		clickHouseQuery(t, c.server, "DROP DATABASE IF EXISTS "+c.database)
		clickHouseQuery(t, c.server, "DROP USER IF EXISTS "+name)
	})
	for _, grant := range grants {
		c.Query(t, fmt.Sprintf("GRANT %s TO %s", grant, name))
	}
	target, err := url.Parse(c.URL)
	if err != nil {
		t.Fatal(err)
	}
	target.User = url.UserPassword(name, password)
	return ClickHouse{URL: target.String(), server: c.server, database: c.database}
}

func clickHouseQuery(t testing.TB, server, query string) string {
	t.Helper()
	status, body := clickHouseAnswer(t, server, query)
	if status != http.StatusOK {
		t.Fatalf("clickhouse %q: %d: %s", query, status, body)
	}
	return body
}

func clickHouseAnswer(t testing.TB, server, query string) (int, string) {
	t.Helper()
	resp, err := oneShotHTTP.Post(server, "text/plain", strings.NewReader(query))
	if err != nil {
		t.Fatalf("clickhouse %q: %v", query, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(body))
}

var oneShotHTTP = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

type dockerUnreachable struct{ reason string }

func (d dockerUnreachable) Error() string { return d.reason }

const dockerUnreachableExit = 3

var onDemandClickHouse = sync.OnceValues(func() (string, error) {
	module, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("find the attn module for scripts/ingest-clickhouse.sh: %w", err)
	}
	script := filepath.Join(filepath.Dir(strings.TrimSpace(string(module))), "scripts", "ingest-clickhouse.sh")
	var stderr bytes.Buffer
	start := exec.Command(script, "start")
	start.Stderr = &stderr
	output, err := start.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == dockerUnreachableExit {
		return "", dockerUnreachable{strings.TrimSpace(stderr.String())}
	}
	if err != nil {
		return "", fmt.Errorf("%s start: %w\n%s", script, err, stderr.String())
	}
	return strings.TrimSpace(string(output)), nil
})

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

type FakeIngest struct {
	URL string

	mu       sync.Mutex
	installs map[string]string
	events   map[string]map[fakeEventKey]bool
	batches  []analytics.Batch
}

type fakeEventKey struct {
	stream analytics.Stream
	seq    int64
}

func StartFakeIngest(t testing.TB) *FakeIngest {
	t.Helper()
	fake := &FakeIngest{installs: map[string]string{}, events: map[string]map[fakeEventKey]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/installs", fake.register)
	mux.HandleFunc("POST /v1/events", fake.acceptEvents)
	mux.HandleFunc("DELETE /v1/installs/me", fake.deleteInstall)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	fake.URL = server.URL
	return fake
}

func (f *FakeIngest) Link(t testing.TB) *IngestLink {
	t.Helper()
	return Ingest{URL: f.URL}.Link(t)
}

func (f *FakeIngest) Installs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	installs := make([]string, 0, len(f.installs))
	for _, install := range f.installs {
		installs = append(installs, install)
	}
	return installs
}

func (f *FakeIngest) Events() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := 0
	for _, events := range f.events {
		stored += len(events)
	}
	return stored
}

func (f *FakeIngest) Batches() []analytics.Batch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]analytics.Batch(nil), f.batches...)
}

func (f *FakeIngest) register(w http.ResponseWriter, _ *http.Request) {
	registration := analytics.Registration{InstallID: "install-" + rand.Text(), Token: rand.Text()}
	f.mu.Lock()
	f.installs[registration.Token] = registration.InstallID
	f.events[registration.InstallID] = map[fakeEventKey]bool{}
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(registration)
}

func (f *FakeIngest) acceptEvents(w http.ResponseWriter, r *http.Request) {
	batch, err := readFakeBatch(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	install, ok := f.installs[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		http.Error(w, `{"error":"unknown install token"}`, http.StatusUnauthorized)
		return
	}
	f.batches = append(f.batches, batch)
	var accepted analytics.Accepted
	for _, record := range batch.Records {
		key := fakeEventKey{record.Stream, record.Seq}
		if f.events[install][key] {
			accepted.Duplicates++
			continue
		}
		f.events[install][key] = true
		accepted.Accepted++
	}
	_ = json.NewEncoder(w).Encode(accepted)
}

func (f *FakeIngest) deleteInstall(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	defer f.mu.Unlock()
	install, ok := f.installs[token]
	if !ok {
		http.Error(w, `{"error":"unknown install token"}`, http.StatusUnauthorized)
		return
	}
	delete(f.installs, token)
	delete(f.events, install)
	w.WriteHeader(http.StatusNoContent)
}

func readFakeBatch(body io.Reader) (analytics.Batch, error) {
	var batch analytics.Batch
	unzipped, err := gzip.NewReader(body)
	if err != nil {
		return batch, err
	}
	lines := json.NewDecoder(unzipped)
	var header struct {
		Build analytics.Build `json:"build"`
	}
	if err := lines.Decode(&header); err != nil {
		return batch, err
	}
	batch.Build = header.Build
	for lines.More() {
		var record analytics.Record
		if err := lines.Decode(&record); err != nil {
			return batch, err
		}
		batch.Records = append(batch.Records, record)
	}
	return batch, nil
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

	registrationHold http.HandlerFunc
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

func (l *IngestLink) HoldNextRegistration() (arrived <-chan struct{}, release func()) {
	reached, released := make(chan struct{}), make(chan struct{})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.registrationHold = func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		select {
		case <-released:
			l.forward(w, r)
		case <-r.Context().Done():
		}
	}
	return reached, sync.OnceFunc(func() { close(released) })
}

func (l *IngestLink) HoldNextEventsReply() (stored <-chan struct{}, release func()) {
	reached, released := make(chan struct{}), make(chan struct{})
	l.injectEventsFault(func(w http.ResponseWriter, r *http.Request) {
		reply := httptest.NewRecorder()
		l.forward(reply, r)
		close(reached)
		select {
		case <-released:
		case <-r.Context().Done():
			return
		}
		for key, values := range reply.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(reply.Code)
		_, _ = w.Write(reply.Body.Bytes())
	})
	return reached, sync.OnceFunc(func() { close(released) })
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
	} else if r.URL.Path == "/v1/installs" && l.registrationHold != nil {
		handle, l.registrationHold = l.registrationHold, nil
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
