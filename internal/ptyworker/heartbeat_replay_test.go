package ptyworker

import (
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/pty"
)

func TestWatchReplaysHeartbeatObservedBeforeTheWatcher(t *testing.T) {
	r := &Runtime{
		cfg:       Config{SessionID: "hb-replay"},
		state:     "working",
		logf:      func(string, ...interface{}) {},
		watchConn: make(map[*connCtx]struct{}),
	}
	observedAt := time.Date(2026, 9, 2, 8, 31, 9, 500000000, time.UTC)
	r.observeState(pty.Observation{
		Source: pty.SourceHeartbeat,
		Claim:  "not_busy",
		Detail: "queue mock ready",
		At:     observedAt,
	})

	conn := &connCtx{runtime: r, authed: true, connID: "1", sendQ: make(chan any, 8)}
	conn.handleRequest(RequestEnvelope{Type: "req", ID: "req-1", Method: MethodWatch})

	var events []EventEnvelope
	for done := false; !done; {
		select {
		case msg := <-conn.sendQ:
			if evt, ok := msg.(EventEnvelope); ok {
				events = append(events, evt)
			}
		default:
			done = true
		}
	}
	if len(events) != 2 {
		t.Fatalf("watch sent %d events, want the state replay then the heartbeat: %+v", len(events), events)
	}
	if *events[0].StateSource != string(pty.SourceWorkerInfo) || *events[0].State != "working" {
		t.Fatalf("first event = %+v, want the cached working state", events[0])
	}
	got := ObservationFromEvent(events[1], *events[1].State, time.Now())
	if got.Source != pty.SourceHeartbeat || got.Claim != "not_busy" || got.Detail != "queue mock ready" {
		t.Fatalf("replayed heartbeat = %+v", got)
	}
	if !got.At.Equal(observedAt) {
		t.Fatalf("replayed heartbeat observed-at = %s, want the original %s", got.At, observedAt)
	}
}

func TestWatchWithoutHeartbeatReplaysOnlyTheState(t *testing.T) {
	r := &Runtime{
		cfg:       Config{SessionID: "no-hb"},
		state:     "working",
		logf:      func(string, ...interface{}) {},
		watchConn: make(map[*connCtx]struct{}),
	}
	conn := &connCtx{runtime: r, authed: true, connID: "1", sendQ: make(chan any, 8)}
	conn.handleRequest(RequestEnvelope{Type: "req", ID: "req-1", Method: MethodWatch})

	count := 0
	for done := false; !done; {
		select {
		case msg := <-conn.sendQ:
			if _, ok := msg.(EventEnvelope); ok {
				count++
			}
		default:
			done = true
		}
	}
	if count != 1 {
		t.Fatalf("watch sent %d events, want only the state replay", count)
	}
}

func TestWatchReplayNeverReordersBehindALiveHeartbeat(t *testing.T) {
	for round := 0; round < 200; round++ {
		r := &Runtime{
			cfg:       Config{SessionID: "hb-race"},
			state:     "working",
			logf:      func(string, ...interface{}) {},
			watchConn: make(map[*connCtx]struct{}),
		}
		base := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
		heartbeat := func(i int) pty.Observation {
			claim := "busy"
			if i%2 == 0 {
				claim = "not_busy"
			}
			return pty.Observation{Source: pty.SourceHeartbeat, Claim: claim, Detail: "t", At: base.Add(time.Duration(i) * time.Millisecond)}
		}
		r.observeState(heartbeat(0))

		conn := &connCtx{runtime: r, authed: true, connID: "1", sendQ: make(chan any, 64)}
		var got []EventEnvelope
		done := make(chan struct{})
		go func() {
			defer close(done)
			for msg := range conn.sendQ {
				if evt, ok := msg.(EventEnvelope); ok && *evt.StateSource == string(pty.SourceHeartbeat) {
					got = append(got, evt)
				}
			}
		}()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 1; i <= 20; i++ {
				r.observeState(heartbeat(i))
			}
		}()
		go func() {
			defer wg.Done()
			conn.handleRequest(RequestEnvelope{Type: "req", ID: "req-1", Method: MethodWatch})
		}()
		wg.Wait()
		close(conn.sendQ)
		<-done

		var last time.Time
		for _, evt := range got {
			at, err := time.Parse(time.RFC3339Nano, *evt.StateObservedAt)
			if err != nil {
				t.Fatalf("round %d: bad observed-at %q", round, *evt.StateObservedAt)
			}
			if at.Before(last) {
				t.Fatalf("round %d: watcher saw heartbeat %s after %s", round, at, last)
			}
			last = at
		}
		if len(got) == 0 {
			t.Fatalf("round %d: watcher saw no heartbeat", round)
		}
	}
}
