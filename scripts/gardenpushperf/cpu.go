package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"nhooyr.io/websocket"
)

type mutation struct {
	name   string
	result string
	msg    func(id string) map[string]any
}

func cpuMutations(seed string) []mutation {
	note := func(ring bool) func(string) map[string]any {
		return func(id string) map[string]any {
			return map[string]any{"cmd": "seed_note", "request_id": id, "seed_id": seed, "body": "garden cpu perf note " + id, "ring": ring}
		}
	}
	check := func(verb string) func(string) map[string]any {
		return func(id string) map[string]any {
			return map[string]any{"cmd": "seed_check", "request_id": id, "seed_id": seed, "verb": verb, "check_id": "C99"}
		}
	}
	move := func(verb string) func(string) map[string]any {
		return func(id string) map[string]any {
			return map[string]any{"cmd": "seed_transition", "request_id": id, "seed_id": seed, "verb": verb}
		}
	}
	return []mutation{
		{"note ring", "seed_note_result", note(true)},
		{"check remove", "seed_check_result", check("remove")},
		{"wither", "seed_transition_result", move("wither")},
		{"note", "seed_note_result", note(false)},
		{"check restore", "seed_check_result", check("restore")},
		{"replant", "seed_transition_result", move("replant")},
	}
}

func measureCPU(ctx context.Context, conn *websocket.Conn, vars, seed string, runs int) {
	for _, verb := range []string{"add", "restore"} {
		write(ctx, conn, map[string]any{"cmd": "seed_check", "request_id": "cpu-setup-" + verb, "seed_id": seed, "verb": verb, "check_id": "C99", "text": "garden cpu perf check"})
		readUntil(ctx, conn, "seed_check_result")
	}
	idle := idleRate(vars, 3*time.Second)
	fmt.Printf("idle: %.2f cpu ms/s\n", idle)
	mutations := cpuMutations(seed)
	var perMutation []float64
	for run := 0; run < runs; run++ {
		start, startCPU := time.Now(), cpuMS(vars)
		for i, m := range mutations {
			id := fmt.Sprintf("cpu-%d-%d", run, i)
			write(ctx, conn, m.msg(id))
			result := readUntil(ctx, conn, m.result)
			var outcome struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
			}
			_ = json.Unmarshal(result.data, &outcome)
			if !outcome.Success {
				fail(fmt.Errorf("%s refused: %s", m.name, outcome.Error))
			}
		}
		endCPU := settle(vars, idle)
		elapsed := time.Since(start).Seconds()
		ms := (float64(endCPU-startCPU) - idle*elapsed) / float64(len(mutations))
		perMutation = append(perMutation, ms)
		fmt.Printf("run %d: %d mutations, %d cpu ms over %.1fs, %.1f cpu ms per mutation above idle\n",
			run+1, len(mutations), endCPU-startCPU, elapsed, ms)
	}
	sorted := slices.Clone(perMutation)
	slices.Sort(sorted)
	fmt.Printf("cpu ms per garden mutation: median %.1f (min %.1f, max %.1f) over %d runs of %d mutations\n",
		sorted[len(sorted)/2], sorted[0], sorted[len(sorted)-1], runs, len(mutations))
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
	fail(fmt.Errorf("daemon did not return to idle within 30s after the mutations"))
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
