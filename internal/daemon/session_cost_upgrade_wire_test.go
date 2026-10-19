package daemon_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

func TestUpgradeFromASingleCostCursorKeepsUnreadRootUsageAndBaselinesExistingChildren(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "inspect the old work")
		agent.reply("Old work. <!-- attn:state=idle -->")
		root := agent.transcript.Path
		cursor, err := transcript.HeadCursor(root)
		if err != nil {
			t.Fatal(err)
		}
		childDir := filepath.Join(root[:len(root)-len(".jsonl")], "subagents")
		if err := os.MkdirAll(childDir, 0700); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(childDir, "agent-old.jsonl")
		appendLegacyCostUsage(t, child, "old-child", "claude-sonnet-4-5", 200, 20)
		w.stop()
		loadLegacySingleCursorCostFixture(t, w, agent.id, cursor)
		appendLegacyCostUsage(t, root, "unread-root", "claude-opus-5", 7, 8)
		w.start()
		w.advance(2 * time.Second)
		usage := listedState(t, w, agent.id).Usage
		if usage == nil || usage.TotalTokens != 15 {
			t.Fatalf("usage after legacy upgrade: %+v, want only the unread root's 15 tokens", usage)
		}
		appendLegacyCostUsage(t, root, "new-root", "claude-opus-5", 3, 4)
		appendLegacyCostUsage(t, child, "new-child", "claude-sonnet-4-5", 5, 6)
		w.advance(2 * time.Second)
		usage = listedState(t, w, agent.id).Usage
		if usage == nil || usage.TotalTokens != 33 {
			t.Fatalf("usage after root and child continued: %+v, want 33 tokens", usage)
		}
		rows := map[string]protocol.SessionUsageModel{}
		for _, row := range usage.Models {
			rows[row.Model] = row
		}
		if root, child := rows["claude-opus-5"], rows["claude-sonnet-4-5"]; root.InputTokens != 10 || root.OutputTokens != 12 || child.InputTokens != 5 || child.OutputTokens != 6 {
			t.Fatalf("upgraded per-source usage: %+v", rows)
		}
	})
}

func loadLegacySingleCursorCostFixture(t *testing.T, w *world, session, cursor string) {
	t.Helper()
	blob, err := json.Marshal(map[string]any{"initialized": true, "cursor": cursor})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(w.Dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE sessions SET session_cost_json = ? WHERE id = ?", string(blob), session); err != nil {
		t.Fatal(err)
	}
}

func appendLegacyCostUsage(t *testing.T, path, id, model string, input, output int) {
	t.Helper()
	line := fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"output_tokens":%d}}}`+"\n", id, model, input, output)
	appendTranscript(t, path, [][]byte{[]byte(line)})
}
