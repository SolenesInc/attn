package main_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARealClaudeSessionThatClosesCompactingCompactsItsConversationThroughTheModAndThenCloses(t *testing.T) {
	t.Parallel()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := startMockModel(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	work := s.Path("shop")
	configDir := filepath.Join(s.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, configDir, work)
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"permissions":{"allow":["Bash(attn agent inbox)","Bash(attn agent close:*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Vars = append(s.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+claude,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+model.URL,
		"ANTHROPIC_API_KEY="+mockModelAPIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	s.Start()
	s.Attn("settings", "set", "claude_mod_enabled", "true")
	const reviewer = "rev-1111-2222"
	register(t, s, reviewer, "reviewer")

	app := s.App()
	worker := s.Spawn(app, fakeagent.Claude, work)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("worker screen:\n%s", s.Attn("agent", "peek", worker).Stdout)
		}
	})
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	const reason = "the checkout fix landed"
	const ring = "attn agent inbox"
	const asked = "wrap up the checkout fix"
	model.runBashAfter(ring, ring)
	model.runBashAfter(asked, `attn agent close --self --compact -m "`+reason+`"`)
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", worker, asked}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == worker
	})
	if got := protocol.Deref(closed.SessionLedgerEntry.CloseReason); got != reason {
		t.Errorf("the session closed with reason %q, want %q", got, reason)
	}
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == worker })

	entries := claudeTranscript(t, configDir)
	closing := slices.IndexFunc(entries, func(e claudeEntry) bool {
		return e.Type == "user" && strings.Contains(e.Text, "closes after this turn, once its conversation is compacted")
	})
	if closing < 0 {
		t.Fatalf("Claude's transcript has no result of the compacting close; entries: %+v", entries)
	}
	boundary := slices.IndexFunc(entries, func(e claudeEntry) bool { return e.Type == "system" && e.Subtype == "compact_boundary" })
	if boundary < 0 || !entries[boundary].Timestamp.After(entries[closing].Timestamp) {
		t.Fatalf("Claude's transcript has no compact boundary after the close was asked at %s; entries: %+v", entries[closing].Timestamp, entries)
	}
	summary := slices.IndexFunc(entries[boundary:], func(e claudeEntry) bool { return e.IsCompactSummary })
	if summary < 0 || !strings.Contains(entries[boundary+summary].Text, mockModelCompactSummary) {
		t.Fatalf("Claude's transcript has no compact summary with the model's summary after its boundary; entries: %+v", entries[boundary:])
	}

	if reopened := s.Attn("session", "reopen", worker, "--action", string(protocol.SessionReopenActionReopen)); reopened.Code != 0 {
		t.Fatalf("session reopen exited %d: %s%s", reopened.Code, reopened.Stdout, reopened.Stderr)
	}
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	mark := model.mark()
	const followUp = "what landed before the close"
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", worker, followUp}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg to the reopened session exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	if opening := model.awaitPromptSince(t, mark, ring); !strings.Contains(opening, mockModelCompactSummary) {
		t.Fatalf("the reopened conversation's first request opens with %q, want Claude's compact summary", opening)
	}
}

type claudeEntry struct {
	Type             string
	Subtype          string
	IsCompactSummary bool
	Timestamp        time.Time
	Text             string
}

func claudeTranscript(t *testing.T, configDir string) []claudeEntry {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(configDir, "projects", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("Claude's transcripts under %s: %q, %v; want one", configDir, files, err)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var entries []claudeEntry
	lines := bufio.NewScanner(f)
	lines.Buffer(nil, 16<<20)
	for lines.Scan() {
		var raw struct {
			Type             string    `json:"type"`
			Subtype          string    `json:"subtype"`
			IsCompactSummary bool      `json:"isCompactSummary"`
			Timestamp        time.Time `json:"timestamp"`
			Message          struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(lines.Bytes(), &raw) != nil {
			continue
		}
		entries = append(entries, claudeEntry{raw.Type, raw.Subtype, raw.IsCompactSummary, raw.Timestamp, contentText(raw.Message.Content)})
	}
	return entries
}
