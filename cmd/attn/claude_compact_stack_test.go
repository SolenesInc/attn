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
	"github.com/victorarias/attn/internal/fakeclaudeapi"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const closingReviewer = "rev-1111-2222"

func realModdedClaude(t *testing.T, vars ...string) (*testworld.Stack, *testworld.Peer, string, *fakeclaudeapi.Model, string) {
	t.Helper()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := fakeclaudeapi.Start(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	work := s.Path("shop")
	configDir := filepath.Join(s.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, configDir, work)
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"permissions":{"allow":["Bash(attn agent inbox)","Bash(attn agent close:*)","Bash(sleep:*)"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Vars = append(s.Vars,
		"ATTN_CLAUDE_EXECUTABLE="+claude,
		"CLAUDE_CONFIG_DIR="+configDir,
		"ANTHROPIC_BASE_URL="+model.URL,
		"ANTHROPIC_API_KEY="+fakeclaudeapi.APIKey,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"DISABLE_AUTOUPDATER=1",
	)
	s.Vars = append(s.Vars, vars...)
	s.Start()
	s.Attn("settings", "set", "claude_mod_enabled", "true")
	register(t, s, closingReviewer, "reviewer")

	app := s.App()
	worker := s.Spawn(app, fakeagent.Claude, work)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("worker screen:\n%s", s.Attn("agent", "peek", worker).Stdout)
		}
	})
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	return s, app, worker, model, configDir
}

func TestARealClaudeSessionThatClosesCompactingCompactsItsConversationThroughTheModAndThenCloses(t *testing.T) {
	t.Parallel()
	s, app, worker, model, configDir := realModdedClaude(t)
	const reviewer = closingReviewer

	const reason = "the checkout fix landed"
	const ring = "attn agent inbox"
	const asked = "wrap up the checkout fix"
	model.RunBashAfter(ring, ring)
	model.RunBashAfter(asked, `attn agent close --self --compact -m "`+reason+`"`)
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
	if summary < 0 || !strings.Contains(entries[boundary+summary].Text, fakeclaudeapi.CompactSummary) {
		t.Fatalf("Claude's transcript has no compact summary with the model's summary after its boundary; entries: %+v", entries[boundary:])
	}

	if reopened := s.Attn("session", "reopen", worker, "--action", string(protocol.SessionReopenActionReopen)); reopened.Code != 0 {
		t.Fatalf("session reopen exited %d: %s%s", reopened.Code, reopened.Stdout, reopened.Stderr)
	}
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	mark := model.Mark()
	const followUp = "what landed before the close"
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", worker, followUp}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg to the reopened session exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	if opening := model.AwaitPromptSince(t, mark, ring); !strings.Contains(opening, fakeclaudeapi.CompactSummary) {
		t.Fatalf("the reopened conversation's first request opens with %q, want Claude's compact summary", opening)
	}
}

func TestARealClaudeSessionThatClosesCompactingWhileAShellRunsInTheBackgroundCompactsAndCloses(t *testing.T) {
	t.Parallel()
	s, app, worker, model, _ := realModdedClaude(t)

	const reason = "the deploy is running"
	const ring = "attn agent inbox"
	const asked = "start the deploy and wrap up"
	model.RunBashAfter(ring, ring)
	model.RunBackgroundBashAfter(asked, "sleep 600")
	model.RunBashAfter("running in background", `attn agent close --self --compact -m "`+reason+`"`)
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", worker, asked}, Session: closingReviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == worker
	})
	if got := protocol.Deref(closed.SessionLedgerEntry.CloseReason); got != reason {
		t.Errorf("the session closed with reason %q, want %q, compacted", got, reason)
	}
	if got := model.CompactCount(); got != 1 {
		t.Errorf("the model was asked for %d compact summaries, want one", got)
	}
}

func TestARealClaudeDraftTypedBeforeACompactingCloseKeepsTheSessionAndTheDraft(t *testing.T) {
	t.Parallel()
	s, app, worker, model, _ := realModdedClaude(t)
	const ring = "attn agent inbox"
	const asked = "wrap up the checkout fix"
	const draft = "please keep this draft"
	model.RunBashAfter(ring, ring)
	model.RunBashAfter(asked, `attn agent close --self --compact -m "the checkout fix landed"`)

	app.Type(worker, draft, "keyboard")
	mark := model.Mark()
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", worker, asked}, Session: closingReviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	model.AwaitPromptSince(t, mark, "once its conversation is compacted")
	testworld.AwaitSession(app, worker, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	mark = model.Mark()
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", worker, "the review is in"}, Session: closingReviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg to the session with the draft exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	model.AwaitPromptSince(t, mark, ring)
	testworld.AwaitSession(app, worker, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	if peek := s.Attn("agent", "peek", worker).Stdout; !strings.Contains(peek, draft) {
		t.Fatalf("the composer lost the draft; the screen shows:\n%s", peek)
	}
	mark = model.Mark()
	app.Type(worker, "\r", "keyboard")
	model.AwaitPromptSince(t, mark, draft)
}

func TestARealClaudePromptTheUserTypedBeforeACompactingCloseDoesNotKeepTheSessionOpen(t *testing.T) {
	t.Parallel()
	_, app, worker, model, _ := realModdedClaude(t)
	const asked = "wrap up the checkout fix"
	const reason = "the checkout fix landed"
	model.RunBashAfter(asked, `attn agent close --self --compact -m "`+reason+`"`)

	app.Type(worker, asked, "keyboard")
	app.Type(worker, "\r", "keyboard")
	closed := testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == worker
	})
	if got := protocol.Deref(closed.SessionLedgerEntry.CloseReason); got != reason {
		t.Errorf("the session closed with reason %q, want %q", got, reason)
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
		entries = append(entries, claudeEntry{raw.Type, raw.Subtype, raw.IsCompactSummary, raw.Timestamp, fakeclaudeapi.ContentText(raw.Message.Content)})
	}
	return entries
}
