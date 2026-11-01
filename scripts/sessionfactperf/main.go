package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

const (
	repriceKey   = "session_cost.billed_as.codex-auto-review"
	repriceValue = "gpt-6-luna"
	quiet        = 750 * time.Millisecond
)

type frame struct {
	at    time.Time
	event string
	data  []byte
}

func main() {
	runs := flag.Int("n", 20, "reprice bursts to measure")
	vars := flag.String("vars", "", "daemon /debug/vars URL (start the daemon with ATTN_PPROF=<port>)")
	pace := flag.Duration("pace", 0, "load mode: send one reprice every pace for -for, and report CPU per second")
	duration := flag.Duration("for", time.Minute, "load mode duration")
	framesOut := flag.String("frames", "", "write every session_state_changed frame of the bursts to this file, one per line")
	flag.Parse()
	if *vars == "" {
		fail(fmt.Errorf("-vars is required"))
	}
	ctx := context.Background()
	url := "ws://localhost:" + config.WSPortForInstance(os.Getenv("ATTN_INSTANCE")) + "/ws"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		fail(err)
	}
	conn.SetReadLimit(256 << 20)
	defer conn.Close(websocket.StatusNormalClosure, "")
	write(ctx, conn, map[string]any{
		"cmd": "client_hello", "client_kind": "wsctl", "version": "protocol-" + protocol.ProtocolVersion,
		"capabilities": []string{}, "client_token": config.ClientToken(),
	})
	frames := make(chan frame, 1<<16)
	go read(ctx, conn, frames)
	waitFor(frames, "initial_state", 60*time.Second)
	drain(frames, 2*time.Second)

	idle := idleRate(*vars, 5*time.Second)
	fmt.Printf("idle: %.2f cpu ms/s\n", idle)

	if *pace > 0 {
		load(ctx, conn, frames, *vars, idle, *pace, *duration)
		return
	}

	var perFact, gaps []float64
	var sizes []int
	var dump []byte
	for run := 0; run < *runs; run++ {
		start, startCPU := time.Now(), cpuMS(*vars)
		reprice(ctx, conn, run)
		burst := drain(frames, quiet)
		endCPU := settle(*vars, idle)
		elapsed := time.Since(start).Seconds()
		facts := burst["session_state_changed"]
		if len(facts) == 0 {
			fail(fmt.Errorf("run %d: no session_state_changed after a reprice; events seen: %v", run+1, counts(burst)))
		}
		ms := (float64(endCPU-startCPU) - idle*elapsed) / float64(len(facts))
		perFact = append(perFact, ms)
		for i, f := range facts {
			sizes = append(sizes, len(f.data))
			dump = append(append(dump, f.data...), '\n')
			if i > 0 {
				gaps = append(gaps, float64(f.at.Sub(facts[i-1].at).Microseconds())/1000)
			}
		}
		fmt.Printf("run %d: %d session facts, events %v, %d cpu ms over %.1fs, %.2f cpu ms per fact above idle\n",
			run+1, len(facts), counts(burst), endCPU-startCPU, elapsed, ms)
	}
	if *framesOut != "" {
		if err := os.WriteFile(*framesOut, dump, 0o644); err != nil {
			fail(err)
		}
	}
	fmt.Printf("cpu ms per session fact (whole daemon, above idle): %s over %d bursts\n", spread(perFact), *runs)
	fmt.Printf("gap between consecutive session_state_changed frames in a burst, ms: %s over %d gaps\n", spread(gaps), len(gaps))
	slices.Sort(sizes)
	fmt.Printf("session_state_changed frame bytes: median %d, min %d, max %d\n", sizes[len(sizes)/2], sizes[0], sizes[len(sizes)-1])
}

func load(ctx context.Context, conn *websocket.Conn, frames chan frame, vars string, idle float64, pace, duration time.Duration) {
	start, startCPU := time.Now(), cpuMS(vars)
	facts := 0
	ticker := time.NewTicker(pace)
	defer ticker.Stop()
	deadline := time.After(duration)
	for i := 0; ; i++ {
		select {
		case <-deadline:
			facts += len(drain(frames, quiet)["session_state_changed"])
			elapsed := time.Since(start).Seconds()
			used := float64(cpuMS(vars) - startCPU)
			fmt.Printf("load: %d session facts in %.0fs (%.1f a minute); daemon %.1f cpu ms/s (idle %.2f), %.2f%% of a core above idle\n",
				facts, elapsed, float64(facts)/elapsed*60, used/elapsed, idle, (used/elapsed-idle)/10)
			return
		case <-ticker.C:
			reprice(ctx, conn, i)
			for len(frames) > 0 {
				if f := <-frames; f.event == "session_state_changed" {
					facts++
				}
			}
		}
	}
}

func reprice(ctx context.Context, conn *websocket.Conn, run int) {
	write(ctx, conn, map[string]any{"cmd": "set_setting", "request_id": fmt.Sprintf("factperf-%d", run), "key": repriceKey, "value": repriceValue})
}

func read(ctx context.Context, conn *websocket.Conn, out chan<- frame) {
	for {
		_, data, err := conn.Read(ctx)
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			return
		}
		if err != nil {
			fail(fmt.Errorf("reading: %w", err))
		}
		var head struct {
			Event   string  `json:"event"`
			Success *bool   `json:"success"`
			Error   *string `json:"error"`
		}
		_ = json.Unmarshal(data, &head)
		if head.Event == "settings_updated" && head.Success != nil && !*head.Success {
			fail(fmt.Errorf("reprice refused: %v", *head.Error))
		}
		out <- frame{at: time.Now(), event: head.Event, data: data}
	}
}

func waitFor(frames <-chan frame, event string, limit time.Duration) {
	deadline := time.After(limit)
	for {
		select {
		case f := <-frames:
			if f.event == event {
				return
			}
		case <-deadline:
			fail(fmt.Errorf("no %s within %s", event, limit))
		}
	}
}

func drain(frames <-chan frame, quiet time.Duration) map[string][]frame {
	seen := map[string][]frame{}
	for {
		select {
		case f := <-frames:
			seen[f.event] = append(seen[f.event], f)
		case <-time.After(quiet):
			return seen
		}
	}
}

func counts(seen map[string][]frame) map[string]int {
	out := map[string]int{}
	for event, frames := range seen {
		out[event] = len(frames)
	}
	return out
}

func spread(xs []float64) string {
	if len(xs) == 0 {
		return "none"
	}
	sorted := slices.Clone(xs)
	sort.Float64s(sorted)
	at := func(q float64) float64 { return sorted[min(len(sorted)-1, int(q*float64(len(sorted))))] }
	return fmt.Sprintf("p50 %.2f, p90 %.2f, p99 %.2f, min %.2f, max %.2f", at(0.5), at(0.9), at(0.99), sorted[0], sorted[len(sorted)-1])
}

func idleRate(vars string, over time.Duration) float64 {
	before := cpuMS(vars)
	time.Sleep(over)
	return float64(cpuMS(vars)-before) / over.Seconds()
}

func settle(vars string, idle float64) int64 {
	const window = time.Second
	last := cpuMS(vars)
	for range 30 {
		time.Sleep(window)
		now := cpuMS(vars)
		if float64(now-last) <= idle*window.Seconds()*1.5+5 {
			return now
		}
		last = now
	}
	fail(fmt.Errorf("daemon did not return to idle within 30s after a reprice"))
	return 0
}

func cpuMS(vars string) int64 {
	resp, err := http.Get(vars)
	if err != nil {
		fail(fmt.Errorf("reading %s: %w", vars, err))
	}
	defer resp.Body.Close()
	var out struct {
		CPUTimeMS int64 `json:"cpu_time_ms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fail(fmt.Errorf("decoding %s: %w", vars, err))
	}
	return out.CPUTimeMS
}

func write(ctx context.Context, conn *websocket.Conn, msg map[string]any) {
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
