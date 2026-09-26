package daemon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
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

func TestSessionUsageTrackerKeepsTheUnreadUsageBehindALegacySingleCursor(t *testing.T) {
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

func TestCodexNewConversationKeepsCostAndPredecessorRollout(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		const id = "codex-new"
		addCostSession(t, d, id, protocol.SessionAgentCodex)
		if err := d.store.InitializeSessionCostTracking(id); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "rollout-old.jsonl")
		oldBytes := []byte(joinUsageLines([]string{codexMeta("native-old", `"cli"`), codexUsageLine("gpt-5.5", 10, 4, 2)}))
		if err := os.WriteFile(oldPath, oldBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if changed, err := d.store.TransitionSessionConversation(id, "native-old", oldPath); err != nil || !changed {
			t.Fatalf("bind old conversation: changed=%t err=%v", changed, err)
		}
		d.startTranscriptWatcherAtPath(id, protocol.SessionAgentCodex, dir, time.Now(), oldPath)
		requireTranscriptDiscovery(t, d, id)
		before, err := d.store.SessionCost(id)
		if err != nil || len(before.Observations) != 1 {
			t.Fatalf("old conversation cost = %+v, err=%v", before, err)
		}
		d.watchersMu.Lock()
		oldWatcher := d.transcriptWatch[id]
		d.watchersMu.Unlock()

		newPath := filepath.Join(dir, "rollout-new.jsonl")
		writeUsageLines(t, newPath, codexMeta("native-new", `"cli"`), codexUsageLine("gpt-5.5", 20, 5, 3))
		d.observeAgentConversation(agentConversationObservation{
			SessionID: id, NativeID: "native-new", TranscriptPath: newPath,
		})
		requireDone(t, oldWatcher.doneCh, "old watcher did not stop after /new")
		d.watchersMu.Lock()
		newWatcher := d.transcriptWatch[id]
		d.watchersMu.Unlock()
		if newWatcher == nil || newWatcher == oldWatcher {
			t.Fatalf("watcher was not rebound after /new: old=%p new=%p", oldWatcher, newWatcher)
		}
		requireTranscriptDiscovery(t, d, id)

		state, err := d.store.SessionCost(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := state.Ledger[sessioncost.AgentKey("gpt-5.5")]; got.InputTokens != 21 || got.CacheReadInputTokens != 9 || got.OutputTokens != 5 {
			t.Fatalf("usage across /new = %+v", got)
		}
		if len(state.Observations) != 2 {
			t.Fatalf("observations across /new = %+v, want both conversations", state.Observations)
		}
		if got, err := os.ReadFile(oldPath); err != nil || !bytes.Equal(got, oldBytes) {
			t.Fatalf("predecessor rollout after /new = %q, err=%v", got, err)
		}
	})
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
	got := state.Ledger[sessioncost.AgentKey("gpt-5.5")]
	if got.InputTokens != 45 || got.CacheReadInputTokens != 15 || got.OutputTokens != 9 {
		t.Fatalf("recursive Codex usage = %+v", got)
	}
	if len(state.Observations) != 3 {
		t.Fatalf("Codex observations include a guardian or miss a descendant: %+v", state.Observations)
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
