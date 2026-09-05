package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

func addCostSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Agent: agent, Label: id, Directory: t.TempDir(),
		State: protocol.StateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func TestSessionUsageWireKeepsKnownCostBesideUnpricedUsage(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "usage", protocol.SessionAgentClaude)
	if got := d.sessionForBroadcast(d.store.Get("usage")); got.Usage != nil {
		t.Fatalf("unused session has usage: %+v", got.Usage)
	}

	observations := []store.SessionCostObservation{
		{ObservationID: "known", Model: "claude-opus-5", Usage: sessioncost.Usage{InputTokens: 4, OutputTokens: 3546}},
		{ObservationID: "unknown", Model: "future-model", Usage: sessioncost.Usage{InputTokens: 11}},
	}
	if _, err := d.store.ApplySessionCostObservations("usage", "cursor", observations); err != nil {
		t.Fatal(err)
	}
	got := d.sessionForBroadcast(d.store.Get("usage"))
	if got.Usage == nil || got.Usage.CostUsd == nil || !got.Usage.HasUnpricedUsage {
		t.Fatalf("mixed-price usage = %+v", got.Usage)
	}
	if got.Usage.TotalTokens != 3561 || len(got.Usage.Models) != 2 {
		t.Fatalf("usage totals = %+v", got.Usage)
	}
}

func TestSessionUsageWireHidesUnreadableDurableState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	persistent, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	d := newTurnDaemon(t)
	_ = d.store.Close()
	d.store = persistent
	t.Cleanup(func() { _ = persistent.Close() })
	addCostSession(t, d, "corrupt", protocol.SessionAgentClaude)

	direct, err := store.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := direct.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ?", `{"ledger":`, "corrupt"); err != nil {
		_ = direct.Close()
		t.Fatal(err)
	}
	_ = direct.Close()

	got := d.store.Get("corrupt")
	d.decorateSessionWithCost(got)
	if got.Usage != nil {
		t.Fatalf("session with unreadable usage state = %+v", got.Usage)
	}
}

func TestSessionUsageMarksUnsupportedDriversUnavailableAndKeepsTheUIBlank(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "copilot", protocol.SessionAgentCopilot)
	w := &transcriptWatcher{sessionID: "copilot", agent: protocol.SessionAgentCopilot}
	batch := transcript.FollowBatch{
		Records: []transcript.FollowRecord{{}},
		Events:  []transcript.Event{{Kind: transcript.EventKindAssistant}},
	}
	if err := d.applySessionUsageAvailability(w, batch); err != nil {
		t.Fatal(err)
	}
	state, err := d.store.SessionCost("copilot")
	if err != nil {
		t.Fatal(err)
	}
	if !state.UsageUnavailable {
		t.Fatalf("unsupported driver usage state = %+v", state)
	}
	if got := d.sessionForBroadcast(d.store.Get("copilot")); got.Usage != nil {
		t.Fatalf("unsupported driver exposed usage = %+v", got.Usage)
	}
}

func TestSessionUsageTrackerIncludesClaudeSubagentsAndRevisions(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "claude", protocol.SessionAgentClaude)
	if err := d.store.InitializeSessionCostTracking("claude"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "root.jsonl")
	childDir := filepath.Join(root[:len(root)-len(".jsonl")], "subagents")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeUsageLines(t, root, claudeUsageLine("root-message", "claude-opus-5", 10, 20))
	child := filepath.Join(childDir, "agent-worker.jsonl")
	writeUsageLines(t, child, claudeUsageLine("child-message", "claude-sonnet-4-5", 30, 40))

	w := &transcriptWatcher{sessionID: "claude", agent: protocol.SessionAgentClaude}
	tracker := d.newSessionUsageTracker(w, root)
	if tracker == nil {
		t.Fatal("Claude did not provide a usage source resolver")
	}
	tracker.Reconcile()
	state, err := d.store.SessionCost("claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Ledger["claude-opus-5"]; got.InputTokens != 10 || got.OutputTokens != 20 {
		t.Fatalf("root usage = %+v", got)
	}
	if got := state.Ledger["claude-sonnet-4-5"]; got.InputTokens != 30 || got.OutputTokens != 40 {
		t.Fatalf("child usage = %+v", got)
	}

	appendUsageLines(t, child, claudeUsageLine("child-message", "claude-sonnet-4-5", 30, 75))
	tracker.Reconcile()
	tracker.Reconcile()
	state, _ = d.store.SessionCost("claude")
	if got := state.Ledger["claude-sonnet-4-5"]; got.InputTokens != 30 || got.OutputTokens != 75 {
		t.Fatalf("revised child usage = %+v", got)
	}

	restarted := d.newSessionUsageTracker(w, root)
	restarted.Reconcile()
	state, _ = d.store.SessionCost("claude")
	if got := state.Ledger["claude-sonnet-4-5"]; got.OutputTokens != 75 {
		t.Fatalf("restart double-counted child usage: %+v", got)
	}
}

func TestSessionUsageTrackerBaselinesAllSourcesWhenResuming(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			testSessionUsageResumeBaseline(t, legacy)
		})
	}
}

func testSessionUsageResumeBaseline(t *testing.T, legacy bool) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "resumed", protocol.SessionAgentClaude)
	root := filepath.Join(t.TempDir(), "resume.jsonl")
	childDir := filepath.Join(root[:len(root)-len(".jsonl")], "subagents")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(childDir, "agent-old.jsonl")
	writeUsageLines(t, root, claudeUsageLine("old-root", "claude-opus-5", 100, 10))
	writeUsageLines(t, child, claudeUsageLine("old-child", "claude-sonnet-4-5", 200, 20))

	if legacy {
		cursor, err := transcript.HeadCursor(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.store.SetSessionCostCursor("resumed", cursor); err != nil {
			t.Fatal(err)
		}
	}

	w := &transcriptWatcher{sessionID: "resumed", agent: protocol.SessionAgentClaude}
	tracker := d.newSessionUsageTracker(w, root)
	tracker.Reconcile()
	state, _ := d.store.SessionCost("resumed")
	if len(state.Ledger) != 0 {
		t.Fatalf("resume backfilled old usage: %+v", state.Ledger)
	}

	appendUsageLines(t, root, claudeUsageLine("new-root", "claude-opus-5", 3, 4))
	appendUsageLines(t, child, claudeUsageLine("new-child", "claude-sonnet-4-5", 5, 6))
	tracker.Reconcile()
	state, _ = d.store.SessionCost("resumed")
	if got := state.Ledger["claude-opus-5"]; got.InputTokens != 3 || got.OutputTokens != 4 {
		t.Fatalf("new root usage = %+v", got)
	}
	if got := state.Ledger["claude-sonnet-4-5"]; got.InputTokens != 5 || got.OutputTokens != 6 {
		t.Fatalf("new child usage = %+v", got)
	}
}

func TestSessionUsageTrackerFollowsCodexLineageRecursively(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "codex", protocol.SessionAgentCodex)
	if err := d.store.InitializeSessionCostTracking("codex"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "09", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root.jsonl")
	child := filepath.Join(dir, "child.jsonl")
	grandchild := filepath.Join(dir, "grandchild.jsonl")
	guardian := filepath.Join(dir, "guardian.jsonl")
	writeUsageLines(t, root, codexMeta("root", `"cli"`), codexUsageLine("gpt-5.5", 10, 4, 2))
	writeUsageLines(t, child, codexMeta("child", codexSpawnSource("root")), codexUsageLine("gpt-5.5", 20, 5, 3))
	writeUsageLines(t, grandchild, codexMeta("grandchild", codexSpawnSource("child")), codexUsageLine("gpt-5.5", 30, 6, 4))
	writeUsageLines(t, guardian, codexMeta("guardian", `{"subagent":{"other":"guardian"}}`), codexUsageLine("gpt-5.5", 999, 0, 1))

	w := &transcriptWatcher{sessionID: "codex", agent: protocol.SessionAgentCodex}
	tracker := d.newSessionUsageTracker(w, root)
	tracker.Reconcile()
	state, _ := d.store.SessionCost("codex")
	got := state.Ledger["gpt-5.5"]
	if got.InputTokens != 45 || got.CacheReadInputTokens != 15 || got.OutputTokens != 9 {
		t.Fatalf("recursive Codex usage = %+v", got)
	}
	if len(state.Observations) != 3 {
		t.Fatalf("Codex observations include a guardian or miss a descendant: %+v", state.Observations)
	}
}

func TestSessionUsageTrackerMarksReplacementIncomplete(t *testing.T) {
	d := newTurnDaemon(t)
	addCostSession(t, d, "replaced", protocol.SessionAgentClaude)
	if err := d.store.InitializeSessionCostTracking("replaced"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "root.jsonl")
	writeUsageLines(t, root, claudeUsageLine("original", "claude-opus-5", 1, 2))
	w := &transcriptWatcher{sessionID: "replaced", agent: protocol.SessionAgentClaude}
	tracker := d.newSessionUsageTracker(w, root)
	tracker.Reconcile()
	writeUsageLines(t, root, claudeUsageLine("replacement-with-longer-id", "claude-opus-5", 9, 9))
	tracker.Reconcile()
	state, _ := d.store.SessionCost("replaced")
	if !state.MeasurementIncomplete {
		t.Fatalf("replacement state = %+v", state)
	}
	got := d.sessionForBroadcast(d.store.Get("replaced"))
	if got.Usage == nil || !protocol.Deref(got.Usage.MeasurementIncomplete) {
		t.Fatalf("wire usage did not carry incomplete measurement: %+v", got.Usage)
	}
}

func writeUsageLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(joinUsageLines(lines)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendUsageLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(joinUsageLines(lines))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append usage: write=%v close=%v", writeErr, closeErr)
	}
}

func joinUsageLines(lines []string) string {
	result := ""
	for _, line := range lines {
		result += line + "\n"
	}
	return result
}

func claudeUsageLine(id, model string, input, output int) string {
	return `{"type":"assistant","message":{"id":"` + id + `","model":"` + model + `","usage":{"input_tokens":` + usageItoa(input) + `,"output_tokens":` + usageItoa(output) + `}}}`
}

func codexMeta(id, source string) string {
	return `{"type":"session_meta","payload":{"id":"` + id + `","source":` + source + `}}`
}

func codexSpawnSource(parent string) string {
	return `{"subagent":{"thread_spawn":{"parent_thread_id":"` + parent + `"}}}`
}

func codexUsageLine(model string, input, cached, output int) string {
	return `{"type":"turn_context","payload":{"model":"` + model + `"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":` + usageItoa(input) + `,"cached_input_tokens":` + usageItoa(cached) + `,"output_tokens":` + usageItoa(output) + `}}}}`
}

func usageItoa(value int) string {
	return fmt.Sprintf("%d", value)
}
