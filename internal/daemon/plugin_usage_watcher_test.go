package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// The same capture internal/transcript prices, read from here so the daemon test
// exercises a real pi session file rather than a shape invented for it.
func piUsageFixtureLines(t *testing.T) [][]byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", "usage", "pi-0.83.0.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.SplitAfter(bytes.TrimRight(data, "\n"), []byte("\n"))
}

func TestPiTranscriptPathPricesTheGuardianBesideTheAgent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-usage", Label: "pi work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	// pi reports the path at session_start and creates the file at its first
	// assistant flush, so the first bytes the tracker sees already hold turn one.
	lines := piUsageFixtureLines(t)
	path := filepath.Join(t.TempDir(), "pi-session.jsonl")
	d.seedPluginUsageBaseline("pi-usage", path)
	if err := os.WriteFile(path, bytes.Join(lines[:5], nil), 0o600); err != nil {
		t.Fatal(err)
	}
	tracker := newSessionUsageTrackerAt(d, "pi-usage", "pi", path, transcript.NewReportedUsageSourceResolver(path))
	tracker.Reconcile()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(bytes.Join(lines[5:], nil)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	tracker.Reconcile()

	decorated := &protocol.Session{ID: "pi-usage"}
	d.decorateSessionWithCost(decorated)
	if decorated.Usage == nil {
		t.Fatal("the reported pi transcript produced no usage")
	}
	var agent, guardian *protocol.SessionUsageModel
	for i := range decorated.Usage.Models {
		row := &decorated.Usage.Models[i]
		switch row.Purpose {
		case "agent":
			agent = row
		case "guardian":
			guardian = row
		}
	}
	if agent == nil || guardian == nil {
		t.Fatalf("models = %+v, want an agent row and a guardian row", decorated.Usage.Models)
	}
	// 17386 covers all six assistant turns: the first flush must not be baselined away.
	if agent.Model != "deepseek-v4-flash" || agent.InputTokens != 17386 || agent.OutputTokens != 1292 {
		t.Fatalf("agent row = %+v", agent)
	}
	if guardian.Model != "deepseek-v4-flash" || guardian.InputTokens != 1746 || guardian.OutputTokens != 122 {
		t.Fatalf("guardian row = %+v", guardian)
	}
	if decorated.Usage.TotalTokens != agent.TotalTokens+guardian.TotalTokens {
		t.Fatalf("total %d excludes the guardian: agent=%d guardian=%d",
			decorated.Usage.TotalTokens, agent.TotalTokens, guardian.TotalTokens)
	}
	// attn has no rate card for this model, so pi's own price is what the row costs.
	if decorated.Usage.CostUsd == nil || decorated.Usage.HasUnpricedUsage {
		t.Fatalf("usage = %+v, want pi's reported cost to price every row", decorated.Usage)
	}
}

func TestPiTranscriptThatAlreadyExistsKeepsItsHeadBaseline(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-resume", Label: "pi work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	lines := piUsageFixtureLines(t)
	path := filepath.Join(t.TempDir(), "pi-session.jsonl")
	if err := os.WriteFile(path, bytes.Join(lines[:5], nil), 0o600); err != nil {
		t.Fatal(err)
	}
	d.seedPluginUsageBaseline("pi-resume", path)
	tracker := newSessionUsageTrackerAt(d, "pi-resume", "pi", path, transcript.NewReportedUsageSourceResolver(path))
	tracker.Reconcile()

	decorated := &protocol.Session{ID: "pi-resume"}
	d.decorateSessionWithCost(decorated)
	if decorated.Usage != nil {
		t.Fatalf("usage = %+v, want a resumed session to bill nothing it inherited", decorated.Usage)
	}
}

func TestPiSessionThatStoppedBeforeItsFirstReplyIsNotMarkedIncomplete(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-quiet", Label: "pi work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	path := filepath.Join(t.TempDir(), "never-written.jsonl")
	d.seedPluginUsageBaseline("pi-quiet", path)
	tracker := newSessionUsageTrackerAt(d, "pi-quiet", "pi", path, transcript.NewReportedUsageSourceResolver(path))
	d.reconcilePluginUsageOnStop(path, false, tracker)

	state, err := d.store.SessionCost("pi-quiet")
	if err != nil {
		t.Fatal(err)
	}
	if state.MeasurementIncomplete {
		t.Fatal("a session whose transcript never appeared was marked measurement-incomplete")
	}
}

func TestPluginReportedTranscriptPathIsStoredAndStartsPricing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-path", Label: "driver work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-path", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	path := filepath.Join(t.TempDir(), "pi-session.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"session\",\"version\":3,\"id\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendPluginMethod(t, client, 3, "session.report_transcript_path", pluginReportTranscriptPathParams{
		SessionID: "pi-path", RunID: "run-1", Path: path,
	})

	if got := d.store.GetAgentDriverTranscriptPath("pi-path"); got != path {
		t.Fatalf("stored transcript path = %q, want %q", got, path)
	}
	d.watchersMu.Lock()
	watcher := d.pluginUsageWatch["pi-path"]
	d.watchersMu.Unlock()
	if watcher == nil || watcher.path != path {
		t.Fatalf("watcher = %+v, want one following the reported path", watcher)
	}

	refusals := []struct {
		name   string
		params pluginReportTranscriptPathParams
	}{
		{"another run", pluginReportTranscriptPathParams{SessionID: "pi-path", RunID: "run-other", Path: path}},
		{"an unknown session", pluginReportTranscriptPathParams{SessionID: "nobody", RunID: "run-1", Path: path}},
		{"a relative path", pluginReportTranscriptPathParams{SessionID: "pi-path", RunID: "run-1", Path: "sessions/s.jsonl"}},
	}
	for i, refusal := range refusals {
		if response := sendPluginMethodResponse(t, client, 4+i, "session.report_transcript_path", refusal.params); response.Error == nil {
			t.Errorf("%s was accepted", refusal.name)
		}
	}
	if got := d.store.GetAgentDriverTranscriptPath("pi-path"); got != path {
		t.Fatalf("a refused report changed the stored path to %q", got)
	}

	d.stopPluginUsageWatcher("pi-path")
}
