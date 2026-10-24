package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"nhooyr.io/websocket"
)

func main() {
	seed := flag.String("seed", "", "seed id to change on each run")
	runs := flag.Int("n", 20, "runs per move")
	moves := flag.String("moves", "note,move", "comma-separated moves: note (a log note), move (wither and replant in turn)")
	out := flag.String("out", "", "write the last garden push of each move to <out>-<move>.json")
	flag.Parse()
	if *seed == "" {
		fail(fmt.Errorf("-seed is required"))
	}
	url := "ws://localhost:" + config.WSPortForInstance(os.Getenv("ATTN_INSTANCE")) + "/ws"
	ctx := context.Background()
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
	first := time.Now()
	initial := readUntil(ctx, conn, "initial_state")
	var state struct {
		Seeds      json.RawMessage `json:"seeds"`
		SeedsTotal int             `json:"seeds_total"`
	}
	_ = json.Unmarshal(initial.data, &state)
	fmt.Printf("initial_state: %d bytes, seeds part %d bytes, total seeds %d, %s\n",
		len(initial.data), len(state.Seeds), state.SeedsTotal, time.Since(first).Round(time.Millisecond))

	for _, move := range strings.Split(*moves, ",") {
		var bytes []int
		var latencies []time.Duration
		var last []byte
		for i := 0; i < *runs; i++ {
			start := time.Now()
			switch move {
			case "note":
				write(ctx, conn, map[string]any{"cmd": "seed_note", "request_id": fmt.Sprintf("perf-%d", i), "seed_id": *seed, "body": fmt.Sprintf("garden push perf note %d", i)})
			case "move":
				verb := "wither"
				if i%2 == 1 {
					verb = "replant"
				}
				write(ctx, conn, map[string]any{"cmd": "seed_transition", "request_id": fmt.Sprintf("perf-%d", i), "seed_id": *seed, "verb": verb})
			default:
				fail(fmt.Errorf("unknown move %q", move))
			}
			push := readUntil(ctx, conn, protocol.EventGardenSeedsUpdated, protocol.EventGardenSeedsChanged)
			latencies = append(latencies, time.Since(start))
			bytes = append(bytes, len(push.data))
			last = push.data
		}
		if *out != "" {
			if err := os.WriteFile(*out+"-"+move+".json", last, 0o644); err != nil {
				fail(err)
			}
		}
		fmt.Printf("%s x%d: push bytes median %d (min %d, max %d); move-to-push latency median %s (min %s, max %s)\n",
			move, *runs, median(bytes), slices.Min(bytes), slices.Max(bytes),
			median(latencies).Round(10*time.Microsecond), slices.Min(latencies).Round(10*time.Microsecond), slices.Max(latencies).Round(10*time.Microsecond))
	}
}

type frame struct {
	event string
	data  []byte
}

func readUntil(ctx context.Context, conn *websocket.Conn, events ...string) frame {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			fail(fmt.Errorf("waiting for %v: %w", events, err))
		}
		var head struct {
			Event string `json:"event"`
		}
		_ = json.Unmarshal(data, &head)
		if slices.Contains(events, head.Event) {
			return frame{event: head.Event, data: data}
		}
	}
}

func write(ctx context.Context, conn *websocket.Conn, msg map[string]any) {
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		fail(err)
	}
}

func median[T int | time.Duration](xs []T) T {
	sorted := slices.Clone(xs)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
