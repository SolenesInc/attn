package main_test

import (
	"os"
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
	exported := seedAs(t, s, "", "export", first.ID, "--out", "-", "--profile", home)
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
		{"edit", first.ID, "-m", strings.Replace(exported, "Home body", "unattributed edit", 1)},
		{"rollback", first.ID},
		{"rollback", first.ID, "--profile", home},
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

func TestAnExplicitProfileSelectsUnresolvedCodexReadsAndASessionRecoversWrites(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	home := app.SelectedProfile()
	shareCodex(t, app)
	parent := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	s.Launched(parent)
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	first := plant(t, s, "Home task", "--profile", home)
	second := plant(t, s, "Side task", "--profile", created.Profile.ID)
	env := []string{"ATTN_INSIDE_APP=1", "ATTN_TERMINAL_ID=", "ATTN_CODEX_SHARED_PROFILE=" + home, "CODEX_THREAD_ID=unresolved-conversation"}
	got := s.Run(testworld.Invocation{Args: []string{"seed", "show", second.ID, "--profile", "Side"}, Env: env})
	if got.Code != 0 || !strings.Contains(got.Stdout, "Side task") {
		t.Errorf("explicit profile without a resolved session: %+v", got)
	}
	got = s.Run(testworld.Invocation{Args: []string{"seed", "show", second.ID, "--profile", "Side", "--session", parent}})
	if got.Code != 1 || !strings.Contains(got.Stderr, "Side") || !strings.Contains(got.Stderr, "Default") {
		t.Errorf("explicit profile and session: %+v; want both profile names in the refusal", got)
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

func TestSeedProfilesCannotOverrideAResolvedSessionOrLoseItsPlot(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	home := app.SelectedProfile()
	shareCodex(t, app)
	plot := plantPlot(t, s, `{"title":"Assigned plot","body":"Complete the assigned steps.","children":[{"title":"Assigned step","body":"Complete this step."},{"title":"Next step","body":"Complete the next step."}]}`)
	loose := plant(t, s, "Outside the plot")
	cwd := s.Path("shop")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	s.SetCodexFolderTrust(cwd, "trusted")
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "assigned", Cwd: cwd, Agent: protocol.Ptr("codex"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(plot.Crown.ID)},
	}
	assigned, err := s.Client().Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	codex := s.Launched(string(assigned.SessionID))
	codex.Prompted()
	codex.Reply("Ready to work on the plot. <!-- attn:state=idle -->")
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !created.Success {
		t.Fatalf("create Side: %+v", created)
	}
	side := plant(t, s, "Side task", "--profile", "Side")
	for _, invocation := range []testworld.Invocation{
		{Session: string(assigned.SessionID)},
		{Env: []string{"ATTN_INSIDE_APP=1", "ATTN_TERMINAL_ID=", "ATTN_CODEX_SHARED_PROFILE=" + home, "CODEX_THREAD_ID=" + codex.ConversationID}},
	} {
		for _, verb := range []string{"show", "note"} {
			invocation.Args = []string{"seed", verb, side.ID, "--profile", "Side"}
			if verb == "note" {
				invocation.Args = append(invocation.Args, "-m", "wrong profile")
			}
			got := s.Run(invocation)
			if got.Code != 1 || !strings.Contains(got.Stderr, "Side") || !strings.Contains(got.Stderr, "Default") {
				t.Errorf("%s with another profile: %+v; want both profile names in the refusal", verb, got)
			}
		}
		for _, profile := range []string{home, "Default"} {
			invocation.Args = []string{"seed", "show", plot.Children[0].ID, "--profile", profile}
			got := s.Run(invocation)
			if got.Code != 0 || !strings.Contains(got.Stdout, "Assigned step") {
				t.Errorf("own profile: %+v", got)
			}
			for _, verb := range []string{"ready", "prime"} {
				invocation.Args = []string{"seed", verb, "--profile", profile}
				got := s.Run(invocation)
				if got.Code != 0 || !strings.Contains(got.Stdout, "Next step") || strings.Contains(got.Stdout, loose.Title) {
					t.Errorf("%s lost the dispatched plot: %+v", verb, got)
				}
			}
		}
	}
	got := s.Attn("seed", "show", side.ID, "--profile", "Side")
	if got.Code != 0 || !strings.Contains(got.Stdout, side.Title) {
		t.Errorf("outside a session: %+v", got)
	}
}
