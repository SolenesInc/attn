package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/fakeclaudeapi"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSeedMailWakesARealClaudeTenderOnStandbyThroughTheMod(t *testing.T) {
	t.Parallel()
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	model := fakeclaudeapi.Start(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	work := s.Path("shop")
	configDir := filepath.Join(s.Dir, "toolhome", ".claude")
	writeClaudeConfig(t, claude, configDir, work)
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"permissions":{"allow":["Bash(attn agent inbox)","Bash(attn seed standby:*)"]}}`), 0o600); err != nil {
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
	s.Start()
	s.Attn("settings", "set", "claude_mod_enabled", "true")
	const reviewer = "rev-1111-2222"
	register(t, s, reviewer, "reviewer")

	app := s.App()
	tender := s.Spawn(app, fakeagent.Claude, work)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("tender screen:\n%s", s.Attn("agent", "peek", tender).Stdout)
		}
	})
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	var seed protocol.Seed
	s.Run(testworld.Invocation{Args: []string{"seed", "plant", "Merge the discount field", "--json"}, Session: tender}).JSON(t, &seed)
	if tend := s.Run(testworld.Invocation{Args: []string{"seed", "tend", seed.ID}, Session: tender}); tend.Code != 0 {
		t.Fatalf("seed tend exited %d: %s", tend.Code, tend.Stderr)
	}

	const note = "On approval: merge the pull request."
	const ring = "attn agent inbox"
	model.RunBashAfter(ring, `attn seed standby `+seed.ID+` -m "`+note+`" --close`)
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", tender, "wait for the review"}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg exited %d: %s%s", sent.Code, sent.Stdout, sent.Stderr)
	}
	testworld.Await(app, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == tender
	})
	if shown := showSeed(t, s, reviewer, seed.ID); shown.Seed.Standby == nil || shown.Seed.Standby.Note != note {
		t.Fatalf("after Claude ran seed standby --close the seed shows %+v, want it on standby with its note", shown.Seed.Standby)
	}

	const approved = "the pull request is approved"
	model.RunBashAfter(note, "attn agent inbox")
	mark := len(app.Log())
	if sent := s.Run(testworld.Invocation{Args: []string{"agent", "msg", seed.ID, approved}, Session: reviewer}); sent.Code != 0 {
		t.Fatalf("attn agent msg %s exited %d: %s%s", seed.ID, sent.Code, sent.Stdout, sent.Stderr)
	}
	testworld.AwaitSessionSince(app, mark, tender, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	model.AwaitPrompt(t, approved)

	if transcripts, _ := filepath.Glob(filepath.Join(configDir, "projects", "*", "*.jsonl")); len(transcripts) != 1 {
		t.Fatalf("Claude wrote transcripts %q, want the woken tender to resume its one conversation", transcripts)
	}
	var woken *claudeUserRow
	rows := claudeUserRows(t, configDir)
	for i, row := range rows {
		if strings.Contains(row.Text, note) && strings.Contains(row.Text, ring) {
			woken = &rows[i]
		}
	}
	if woken == nil {
		t.Fatalf("Claude's transcript has no user row with the standby note and %q; rows: %+v", ring, rows)
	}
	if woken.Origin.Kind != "plugin" || woken.Origin.Name != "attn" {
		t.Fatalf("Claude recorded the wake with origin %+v, want the attn plugin's; it was typed, not handed to the mod", woken.Origin)
	}
	if shown := showSeed(t, s, reviewer, seed.ID); shown.Seed.Standby != nil {
		t.Errorf("after the wake delivered the seed shows standby %+v, want it ended", shown.Seed.Standby)
	}
}
