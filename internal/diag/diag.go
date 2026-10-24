package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"time"
)

type Stats struct {
	Sessions         int            `json:"sessions"`
	PtyBackend       string         `json:"pty_backend"`
	WorkerPIDs       map[string]int `json:"worker_pids,omitempty"`
	DocSubscriptions int            `json:"doc_subscriptions"`
}

type StatsFunc func() Stats

type Server struct {
	httpServer *http.Server
	addr       string
	stats      StatsFunc
}

func Start(addr string, stats StatsFunc) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("diag: listen %s: %w", addr, err)
	}
	s := &Server{addr: ln.Addr().String(), stats: stats}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/vars", s.handleVars)
	mux.HandleFunc("/", s.handleIndex)

	s.httpServer = &http.Server{Handler: mux}
	go func() { _ = s.httpServer.Serve(ln) }()
	return s, nil
}

func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	return s.addr
}

func (s *Server) Close() error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleVars(w http.ResponseWriter, _ *http.Request) {
	var stats Stats
	if s.stats != nil {
		stats = s.stats()
	}
	process := Sample()

	out := map[string]any{
		"pid":               os.Getpid(),
		"goroutines":        process.Goroutines,
		"resident_bytes":    process.ResidentBytes,
		"cpu_time_ms":       process.CPUTimeMS,
		"open_fds":          process.OpenFDs,
		"gomaxprocs":        runtime.GOMAXPROCS(0),
		"num_cpu":           runtime.NumCPU(),
		"go_version":        runtime.Version(),
		"sessions":          stats.Sessions,
		"pty_backend":       stats.PtyBackend,
		"worker_pids":       stats.WorkerPIDs,
		"doc_subscriptions": stats.DocSubscriptions,
		"memstats": map[string]any{
			"heap_alloc":        process.HeapAlloc,
			"heap_sys":          process.HeapSys,
			"heap_idle":         process.HeapIdle,
			"heap_inuse":        process.HeapInuse,
			"heap_released":     process.HeapReleased,
			"heap_objects":      process.HeapObjects,
			"sys":               process.Sys,
			"next_gc":           process.NextGC,
			"num_gc":            process.NumGC,
			"gc_pause_total_ns": process.GCPauseTotal,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "attn diagnostics\n\n"+
		"/debug/vars    runtime + session/worker snapshot (JSON)\n"+
		"/debug/pprof/  Go pprof index (heap, goroutine, profile, trace, ...)\n")
}
