package ingest

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

type metrics struct {
	accepted   atomic.Int64
	duplicates atomic.Int64
	registered atomic.Int64
	mu         sync.Mutex
	requests   map[routeStatus]int64
	refusals   map[string]int64
}

type routeStatus struct {
	route  string
	status int
}

func newMetrics() *metrics {
	return &metrics{requests: map[routeStatus]int64{}, refusals: map[string]int64{}}
}

func (m *metrics) request(route string, status int) {
	m.mu.Lock()
	m.requests[routeStatus{route, status}]++
	m.mu.Unlock()
}

func (m *metrics) refusal(limit string) {
	m.mu.Lock()
	m.refusals[limit]++
	m.mu.Unlock()
}

func (m *metrics) serve(w http.ResponseWriter, _ *http.Request) {
	var out strings.Builder
	counter := func(name, help string, value int64) {
		fmt.Fprintf(&out, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, value)
	}
	counter("attn_ingest_events_accepted_total", "Records stored.", m.accepted.Load())
	counter("attn_ingest_events_duplicate_total", "Records already stored for their install, stream and seq.", m.duplicates.Load())
	counter("attn_ingest_installs_registered_total", "Installs registered.", m.registered.Load())

	m.mu.Lock()
	requests := make([]string, 0, len(m.requests))
	for key, n := range m.requests {
		requests = append(requests, fmt.Sprintf("attn_ingest_requests_total{route=%q,status=\"%d\"} %d\n", key.route, key.status, n))
	}
	refusals := make([]string, 0, len(m.refusals))
	for limit, n := range m.refusals {
		refusals = append(refusals, fmt.Sprintf("attn_ingest_refusals_total{limit=%q} %d\n", limit, n))
	}
	m.mu.Unlock()
	slices.Sort(requests)
	slices.Sort(refusals)
	out.WriteString("# HELP attn_ingest_requests_total Requests by route and response status.\n# TYPE attn_ingest_requests_total counter\n")
	out.WriteString(strings.Join(requests, ""))
	out.WriteString("# HELP attn_ingest_refusals_total Requests refused by a limit, by limit name.\n# TYPE attn_ingest_refusals_total counter\n")
	out.WriteString(strings.Join(refusals, ""))

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(out.String()))
}
