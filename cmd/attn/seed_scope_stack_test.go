package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSharedCodexSeedReadsUseTheirProfileWithoutACurrentSession(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	home := app.SelectedProfile()
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	first := plant(t, s, "Home task", "--profile", home, "-m", "Home body")
	plant(t, s, "Side task", "--profile", created.Profile.ID)
	if got := s.Attn("seed", "note", first.ID, "-m", "Home note", "--profile", home); got.Code != 0 {
		t.Fatalf("write fixture note: %+v", got)
	}
	env := []string{"ATTN_INSIDE_APP=1", "ATTN_TERMINAL_ID=", "ATTN_CODEX_SHARED_PROFILE=" + home, "CODEX_THREAD_ID=unresolved-conversation"}
	for _, row := range []struct {
		args []string
		want string
	}{
		{[]string{"show", first.ID}, "Home task"},
		{[]string{"notes", first.ID}, "Home note"},
		{[]string{"ls"}, "Home task"},
		{[]string{"search", "task"}, "Home task"},
		{[]string{"ready"}, "Home task"},
		{[]string{"export", first.ID, "--out", "-"}, "Home body"},
		{[]string{"prime"}, "One seed is ready now."},
		{[]string{"review", "show"}, "0 seeds need review"},
	} {
		got := s.Run(testworld.Invocation{Args: append([]string{"seed"}, row.args...), Env: env})
		if got.Code != 0 || !strings.Contains(got.Stdout, row.want) || strings.Contains(got.Stdout, "Side task") {
			t.Errorf("read %q: %+v, want %q in the home profile", row.args, got, row.want)
		}
	}
	for _, args := range [][]string{
		{"note", first.ID, "-m", "unattributed note"},
		{"edit", first.ID, "-m", "unattributed edit"},
		{"review", "start"},
		{"note", first.ID, "-m", "profile alone cannot identify an author", "--profile", home},
	} {
		got := s.Run(testworld.Invocation{Args: append([]string{"seed"}, args...), Env: env})
		if got.Code != 1 || !strings.Contains(got.Stderr, "--session") || !strings.Contains(got.Stderr, "--profile") {
			t.Errorf("mutation %q: %+v, want a session refusal with scope guidance", args, got)
		}
	}
	shown := s.Attn("seed", "show", first.ID, "--profile", home)
	if shown.Code != 0 || !strings.Contains(shown.Stdout, "Home body") || strings.Contains(shown.Stdout, "unattributed") {
		t.Errorf("refused mutations changed the seed: %+v", shown)
	}
}

func TestAnExplicitProfileOverridesASharedCodexSeedReadAndASessionRecoversWrites(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	home := app.SelectedProfile()
	shareCodex(t, app)
	parent := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(parent)
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	first := plant(t, s, "Home task", "--profile", home)
	second := plant(t, s, "Side task", "--profile", created.Profile.ID)
	for _, conversation := range []string{codex.ConversationID, "unresolved-conversation"} {
		env := []string{"ATTN_INSIDE_APP=1", "ATTN_TERMINAL_ID=", "ATTN_CODEX_SHARED_PROFILE=" + home, "CODEX_THREAD_ID=" + conversation}
		got := s.Run(testworld.Invocation{Args: []string{"seed", "show", second.ID, "--profile", "Side"}, Env: env})
		if got.Code != 0 || !strings.Contains(got.Stdout, "Side task") {
			t.Errorf("explicit profile from %s: %+v", conversation, got)
		}
	}
	got := s.Run(testworld.Invocation{Args: []string{"seed", "show", second.ID, "--profile", "Side", "--session", parent}})
	if got.Code != 0 || !strings.Contains(got.Stdout, "Side task") {
		t.Errorf("explicit profile and session: %+v", got)
	}
	got = s.Run(testworld.Invocation{Args: []string{"seed", "note", first.ID, "-m", "Recovered writer", "--session", parent}, Env: []string{"ATTN_INSIDE_APP=1", "ATTN_TERMINAL_ID=", "ATTN_CODEX_SHARED_PROFILE=" + home, "CODEX_THREAD_ID=unresolved-conversation"}})
	if got.Code != 0 {
		t.Fatalf("explicit session: %+v", got)
	}
	notes, err := s.Client().WithGardenProfile(home, "").SeedNotes("", first.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes.Notes) != 1 || notes.Notes[0].Body != "Recovered writer" || string(notes.Notes[0].AuthorSession) != parent {
		t.Errorf("recovered write: %+v", notes.Notes)
	}
}
