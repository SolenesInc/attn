package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestATakeoverCountsARequestCopiedAcrossClaudeTranscriptsOnlyOnce(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	cwd := w.Path("shop")
	original := w.Spawn(app, fakeagent.Claude, cwd)
	a := w.Launched(original)
	app.TypeLine(original, "original question")
	a.Prompted()
	firstReply := "original answer <!-- attn:state=idle -->"
	a.Reply(firstReply)
	awaitUsageTokens(app, original, claudeTokens(firstReply))
	paths, err := filepath.Glob(filepath.Join(w.Dir, "toolhome", ".claude", "projects", "*", a.ConversationID+".jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("original transcript: %v, %v", paths, err)
	}
	originalPath := paths[0]
	copied, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	forked := w.Spawn(app, fakeagent.Claude, cwd)
	b := w.Launched(forked)
	forkPath := filepath.Join(filepath.Dir(originalPath), b.ConversationID+".jsonl")
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(copied)), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		row["sessionId"] = b.ConversationID
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(forkPath, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app.TypeLine(forked, "fork question")
	b.Prompted()
	forkReply := "fork answer <!-- attn:state=idle -->"
	b.Reply(forkReply)
	counted := claudeTokens(firstReply) + claudeTokens(forkReply)
	awaitUsageTokens(app, forked, counted)
	terminal := protocol.TerminalID(app.Terminal(forked))
	if err := cli.ObserveAgentConversation(terminal, a.ConversationID, originalPath); err != nil {
		t.Fatal(err)
	}
	_, caller, err := cli.QueryAs(terminal)
	if err != nil || caller != protocol.SessionID(forked) {
		t.Fatalf("takeover replaced session %s with %s: %v", forked, caller, err)
	}
	app.TypeLine(original, "original follow-up")
	a.Prompted()
	followup := "new original answer <!-- attn:state=idle -->"
	a.Reply(followup)
	usage := awaitUsageTokens(app, forked, counted+claudeTokens(followup))
	t.Logf("after takeover, %d tokens: original+fork+follow-up, copied request counted once", usage.TotalTokens)
}

func TestATakeoverKeepsUnreadForkUsageAndItsDeduplicationAcrossRestart(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		original := w.bubbleClaude(t, app, "shop")
		app.TypeLine(original.id, "original question")
		firstReply := "original answer <!-- attn:state=idle -->"
		original.reply(firstReply)
		w.advance(time.Second)
		forked := w.bubbleClaude(t, app, "shop")
		copied, err := os.ReadFile(original.transcript.Path)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, line := range strings.Split(strings.TrimSpace(string(copied)), "\n") {
			var row map[string]any
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatal(err)
			}
			row["sessionId"] = forked.transcript.ConversationID
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(raw))
		}
		if err := os.WriteFile(forked.transcript.Path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		app.TypeLine(forked.id, "fork question")
		forkReply := "fork answer <!-- attn:state=idle -->"
		forked.reply(forkReply)
		w.advance(time.Second)
		appendLegacyCostUsage(t, forked.transcript.Path, "unread-final-fork-request", "claude-opus-4-8", 7, 8)
		if err := w.Client().ObserveAgentConversation(protocol.TerminalID(forked.self), original.transcript.ConversationID, original.transcript.Path); err != nil {
			t.Fatal(err)
		}
		expected := claudeTokens(firstReply) + claudeTokens(forkReply) + 15
		if usage := queriedSession(t, w.Client(), forked.id).Usage; usage == nil || usage.TotalTokens != expected {
			t.Fatalf("usage after takeover=%+v, want %d including unread fork tail", usage, expected)
		}
		appendLegacyCostUsage(t, original.transcript.Path, "after-takeover", "claude-opus-4-8", 3, 4)
		w.advance(time.Second)
		expected += 7
		if usage := queriedSession(t, w.Client(), forked.id).Usage; usage == nil || usage.TotalTokens != expected {
			t.Fatalf("usage after followup=%+v, want %d with copied request counted once", usage, expected)
		}
		w.restart()
		if usage := queriedSession(t, w.Client(), forked.id).Usage; usage == nil || usage.TotalTokens != expected {
			t.Fatalf("usage after restart=%+v, want %d", usage, expected)
		}
	})
}
