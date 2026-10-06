package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/transcript"
)

func addCostSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: protocol.SessionID(id), Agent: agent, Label: id, Directory: t.TempDir(),
		State: protocol.StateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func TestSessionUsageTrackerKeepsTheUnreadUsageBehindALegacySingleCursor(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addCostSession(t, d, "resumed", protocol.SessionAgentClaude)
	root := filepath.Join(t.TempDir(), "resume.jsonl")
	childDir := filepath.Join(root[:len(root)-len(".jsonl")], "subagents")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(childDir, "agent-old.jsonl")
	writeUsageLines(t, root, claudeUsageLine("old-root", "claude-opus-5", 100, 10))
	writeUsageLines(t, child, claudeUsageLine("old-child", "claude-sonnet-4-5", 200, 20))
	cursor, err := transcript.HeadCursor(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.SetSessionCostCursor("resumed", cursor); err != nil {
		t.Fatal(err)
	}
	appendUsageLines(t, root, claudeUsageLine("unread-root", "claude-opus-5", 7, 8))

	w := &transcriptWatcher{sessionID: "resumed", agent: protocol.SessionAgentClaude}
	tracker := d.newSessionUsageTracker(w, root)
	tracker.Reconcile()
	state, _ := d.store.SessionCost("resumed")
	if got := state.Ledger[sessioncost.AgentKey("claude-opus-5")]; got.InputTokens != 7 || got.OutputTokens != 8 || len(state.Ledger) != 1 {
		t.Fatalf("the upgrade must keep the unread root usage without the old child history: %+v", state.Ledger)
	}

	appendUsageLines(t, root, claudeUsageLine("new-root", "claude-opus-5", 3, 4))
	appendUsageLines(t, child, claudeUsageLine("new-child", "claude-sonnet-4-5", 5, 6))
	tracker.Reconcile()
	state, _ = d.store.SessionCost("resumed")
	if got := state.Ledger[sessioncost.AgentKey("claude-opus-5")]; got.InputTokens != 10 || got.OutputTokens != 12 {
		t.Fatalf("new root usage = %+v", got)
	}
	if got := state.Ledger[sessioncost.AgentKey("claude-sonnet-4-5")]; got.InputTokens != 5 || got.OutputTokens != 6 {
		t.Fatalf("new child usage = %+v", got)
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

func usageItoa(value int) string {
	return fmt.Sprintf("%d", value)
}
