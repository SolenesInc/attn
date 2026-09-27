package daemon_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func piTranscriptLines(t *testing.T) [][]byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", "usage", "pi-0.83.0.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.SplitAfter(bytes.TrimRight(data, "\n"), []byte("\n"))
}

func appendTranscript(t *testing.T, path string, lines [][]byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(bytes.Join(lines, nil)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func reportTranscript(driver *driverPeer, session, runID, path string) error {
	return driver.report("session.report_transcript_path", map[string]any{"session_id": session, "run_id": runID, "path": path})
}

func usageRow(usage *protocol.SessionUsage, purpose string) *protocol.SessionUsageModel {
	for i := range usage.Models {
		if usage.Models[i].Purpose == purpose {
			return &usage.Models[i]
		}
	}
	return nil
}

func awaitPricedRows(app *testworld.Peer, session string, agentInput int) *protocol.SessionUsage {
	app.T.Helper()
	return testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		if s.Usage == nil {
			return false
		}
		agent := usageRow(s.Usage, "agent")
		return agent != nil && agent.InputTokens == agentInput && usageRow(s.Usage, "guardian") != nil
	}).Usage
}

func TestAPiSessionIsPricedFromTheTranscriptItsDriverReportsGuardianIncluded(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "attn-pi", "pi", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "pi")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	path := w.Path("pi-session.jsonl")
	for _, refused := range []struct {
		name, session, run, path string
	}{
		{name: "for another run", session: session, run: "run-other", path: path},
		{name: "for an unknown session", session: "nobody", run: run.RunID, path: path},
		{name: "with a relative path", session: session, run: run.RunID, path: "sessions/s.jsonl"},
	} {
		if err := reportTranscript(driver, refused.session, refused.run, refused.path); err == nil {
			t.Errorf("a transcript report %s was accepted", refused.name)
		}
	}
	if err := reportTranscript(driver, session, run.RunID, path); err != nil {
		t.Fatal(err)
	}

	lines := piTranscriptLines(t)
	appendTranscript(t, path, lines[:5])
	appendTranscript(t, path, lines[5:])
	usage := awaitPricedRows(app, session, 17386)
	agent, guardian := usageRow(usage, "agent"), usageRow(usage, "guardian")
	if agent.Model != "deepseek-v4-flash" || agent.OutputTokens != 1292 {
		t.Errorf("the agent row = %+v, want deepseek-v4-flash with 1292 output tokens", agent)
	}
	if guardian.Model != "deepseek-v4-flash" || guardian.InputTokens != 1746 || guardian.OutputTokens != 122 {
		t.Errorf("the guardian row = %+v, want deepseek-v4-flash at 1746 in and 122 out", guardian)
	}
	if usage.TotalTokens != agent.TotalTokens+guardian.TotalTokens || usage.CostUsd == nil || usage.HasUnpricedUsage {
		t.Errorf("the usage = %+v, want every row priced from pi's reported cost and totalled", usage)
	}
}

func TestAPiSessionThatEndsBeforeItsTranscriptAppearsIsNotIncomplete(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "attn-pi", "pi", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "pi")
	cwd := w.Path("shop")
	session, run := spawnDriven(w, app, driver, cwd)
	if err := reportTranscript(driver, session, run.RunID, w.Path("never-written.jsonl")); err != nil {
		t.Fatal(err)
	}
	exitDriven(app, driver, session)

	relaunch := relaunchDriven(w, driver, session, cwd)
	path := w.Path("pi-session.jsonl")
	if err := reportTranscript(driver, session, relaunch.RunID, path); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, path, piTranscriptLines(t))
	if usage := awaitPricedRows(app, session, 17386); protocol.Deref(usage.MeasurementIncomplete) {
		t.Errorf("the usage = %+v, want the run that ended before writing anything not to mark it incomplete", usage)
	}
}
