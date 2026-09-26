package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/statetrace"
)

func newTraceDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
}

func traceOf(t *testing.T, d *Daemon, sessionID string) []statetrace.Observation {
	t.Helper()
	return d.stateTraceRecorder().Observations(sessionID)
}

func onlyObservation(t *testing.T, d *Daemon, sessionID string) statetrace.Observation {
	t.Helper()
	got := traceOf(t, d, sessionID)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 observation, got %d: %+v", len(got), got)
	}
	return got[0]
}

func heartbeatObs(claim, detail string, at time.Time) pty.Observation {
	return pty.Observation{Source: pty.SourceHeartbeat, Claim: claim, Detail: detail, At: at}
}

func TestTraceCollapsesHeartbeatsAcrossSpinnerFrames(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-spinner-frames"
	addCharacterizationSession(t, d, id, protocol.SessionAgentClaude, protocol.SessionStateWorking)

	start := time.Now().Add(-30 * time.Second)
	frames := []string{"⠐", "⠸", "⠿", "⠇", "⠏", "⠋", "⠙"}
	for i, frame := range frames {
		at := start.Add(time.Duration(i) * time.Second)
		_ = frame
		d.handlePTYState(id, heartbeatObs("busy", "Run background sleep command", at))
	}

	got := onlyObservation(t, d, id)
	if got.Repeats != len(frames)-1 {
		t.Fatalf("Repeats %d, want %d — one row for the whole turn", got.Repeats, len(frames)-1)
	}
	if got.Claim != "busy" {
		t.Fatalf("claim %q, want busy", got.Claim)
	}
	if got.Detail != "Run background sleep command" {
		t.Fatalf("detail %q, want the frame-free summary", got.Detail)
	}
	if want := start.Add(time.Duration(len(frames)-1) * time.Second); !got.ObservedAt.Equal(want) {
		t.Fatalf("ObservedAt %s, want the newest %s", got.ObservedAt, want)
	}

	d.handlePTYState(id, heartbeatObs("busy", "Editing files", start.Add(30*time.Second)))
	if all := traceOf(t, d, id); len(all) != 2 {
		t.Fatalf("a changed summary must open a new row: %+v", all)
	}

	withFrames := newTraceDaemon(t)
	framed := "sess-spinner-unstripped"
	addCharacterizationSession(t, withFrames, framed, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	for i, frame := range frames {
		at := start.Add(time.Duration(i) * time.Second)
		withFrames.handlePTYState(framed, heartbeatObs("busy", frame+" Run background sleep command", at))
	}
	if all := traceOf(t, withFrames, framed); len(all) != len(frames) {
		t.Fatalf("volatile details collapsed unexpectedly (%d rows for %d frames); "+
			"the ring pressure this test guards against would be invisible", len(all), len(frames))
	}
}

func callHandler(t *testing.T, call func(net.Conn)) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(server)
		_ = server.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	_ = client.Close()
	<-done
	return resp
}
