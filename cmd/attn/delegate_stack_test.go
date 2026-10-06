package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func gitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "README.md"},
		{"-c", "user.name=attn", "-c", "user.email=attn@example.invalid", "commit", "-q", "-m", "start"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
}

func saveDelegationPreferences(t *testing.T, app *testworld.Peer, preferences protocol.DelegationPreferences) {
	t.Helper()
	id := uuid.NewString()
	saved := testworld.Request(app, protocol.DelegationPreferencesSaveMessage{Cmd: protocol.CmdDelegationPreferencesSave, RequestID: id, Preferences: preferences},
		protocol.EventDelegationPreferencesResult, func(m protocol.DelegationPreferencesResultMessage) bool { return m.RequestID == id })
	if !saved.Success {
		t.Fatalf("saving delegation preferences: %s", protocol.Deref(saved.Error))
	}
}

func pinned(argv []string, flag string) string {
	if i := slices.Index(argv, flag); i >= 0 && i+1 < len(argv) {
		return argv[i+1]
	}
	return ""
}

type delegated struct {
	Agent     string `json:"agent"`
	Branch    string `json:"branch"`
	Checkout  string `json:"checkout"`
	Directory string `json:"directory"`
	Effort    string `json:"effort"`
	Model     string `json:"model"`
	SeedID    string `json:"seed_id"`
	SessionID string `json:"session_id"`
}

func TestDelegateStartsTheRequestItsFlagsDescribeAndRefusesRetiredOnes(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	repo := s.Path("shop")
	gitRepo(t, repo)
	source := s.Spawn(app, fakeagent.Claude, repo)
	s.Launched(source)

	started := s.Run(testworld.Invocation{Session: source, Args: []string{"delegate",
		"--brief", "Investigate the parser", "--cwd", repo,
		"--new-worktree", "--branch", "feat/parser", "--from", "main",
		"--model", "default", "--effort", "high"}})
	if started.Code != 0 {
		t.Fatalf("attn delegate exited %d: %s", started.Code, started.Stderr)
	}
	var result delegated
	started.JSON(t, &result)
	if result.Agent != "claude" || result.Branch != "feat/parser" || result.Checkout != "created" || result.Model != "" || result.Effort != "high" {
		t.Errorf("delegate printed %+v, want the source session's claude on a new feat/parser worktree at the agent's default model and high effort", result)
	}
	testworld.AwaitSession(app, result.SessionID, func(x protocol.Session) bool {
		return string(protocol.Deref(x.DispatcherSessionID)) == source && protocol.Deref(x.Branch) == "feat/parser" && x.Directory == result.Directory
	})
	run := s.Launched(result.SessionID)
	if i := slices.Index(run.Argv, "--effort"); i < 0 || run.Argv[i+1] != "high" || slices.Contains(run.Argv, "--model") {
		t.Errorf("the delegated claude ran %q, want effort high and no model pin", run.Argv)
	}
	if got := run.Prompted(); !strings.Contains(got, result.SeedID) {
		t.Errorf("the delegated claude started on %q, want its seed %s", got, result.SeedID)
	}
	if seed := s.Attn("seed", "show", result.SeedID).Stdout; !strings.Contains(seed, "Investigate the parser") {
		t.Errorf("seed %s does not carry the brief:\n%s", result.SeedID, seed)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--workspace", "old"}, want: "--workspace and --new-workspace retired"},
		{args: []string{"--seed", "s-abc123"}, want: "pass exactly one of --brief, --brief-file, or --seed"},
		{args: []string{"--handover"}, want: "--handover requires --seed"},
		{args: []string{"--choice", "hard"}, want: "--choice requires --role"},
		{args: []string{"--role", "builder", "--fallback"}, want: "--role and --fallback cannot be combined"},
		{args: []string{"--branch", "feat/ignored"}, want: "--branch requires --reuse-checkout or --new-worktree"},
		{args: []string{"--existing-branch", "feat/ignored"}, want: "--existing-branch requires --reuse-checkout or --new-worktree"},
		{args: []string{"--from", "origin/next"}, want: "--from requires --reuse-checkout or --new-worktree"},
		{args: []string{"--worktree-path", "/tmp/ignored"}, want: "--worktree-path requires --reuse-checkout or --new-worktree"},
		{args: []string{"--allow-worktree-reuse"}, want: "--allow-worktree-reuse requires --reuse-checkout or --new-worktree"},
		{args: []string{"--new-worktree", "--branch", "feat/x"}, want: "--new-worktree --branch requires --from"},
		{args: []string{"--model", ""}, want: "--model is required"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			args := append([]string{"delegate", "--brief", "Task", "--cwd", repo, "--model", "opus"}, tc.args...)
			got := s.Run(testworld.Invocation{Session: source, Args: args})
			if got.Code != 2 || !strings.Contains(got.Stderr, tc.want) || got.Stdout != "" {
				t.Errorf("exited %d with stderr %q, want 2 and %q", got.Code, got.Stderr, tc.want)
			}
		})
	}

	notes := s.Path("notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	saveDelegationPreferences(t, app, protocol.DelegationPreferences{
		Enabled: true,
		Roles: []protocol.DelegationRole{{
			ID: "builder", Name: "Builder", Enabled: true, Description: "Implement changes", DefaultChoiceID: "everyday",
			Choices: []protocol.DelegationChoice{
				{ID: "everyday", Name: "Everyday", Selection: protocol.DelegationSelection{Harness: "claude", Effort: "low"}},
				{ID: "hard", Name: "Hard", When: "the change spans packages", Selection: protocol.DelegationSelection{Harness: "claude", Model: "sonnet", Effort: "medium"}},
			},
		}},
		Fallback: protocol.DelegationFallback{Selection: protocol.DelegationSelection{Harness: "claude", Effort: "high"}},
	})
	for i, tc := range []struct {
		name   string
		args   []string
		effort string
	}{
		{name: "the hard choice moved to the agent's default model", args: []string{"--role", "builder", "--choice", "hard", "--model", "default"}},
		{name: "the default choice at a pinned effort", args: []string{"--role", "builder", "--effort", "high"}, effort: "high"},
		{name: "the fallback at the agent's default effort", args: []string{"--fallback", "--effort", "default"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"delegate", "--brief", "Build the discount field", "--cwd", notes, "--name", fmt.Sprintf("delegate-%d", i)}, tc.args...)
			started := s.Run(testworld.Invocation{Session: source, Args: args})
			if started.Code != 0 {
				t.Fatalf("attn delegate exited %d: %s", started.Code, started.Stderr)
			}
			var result delegated
			started.JSON(t, &result)
			if result.Agent != "claude" || result.Model != "" || result.Effort != tc.effort {
				t.Errorf("delegate printed %+v, want claude on its default model at effort %q", result, tc.effort)
			}
			run := s.Launched(result.SessionID)
			if argv := run.Argv; slices.Contains(argv, "--model") || pinned(argv, "--effort") != tc.effort || slices.Contains(argv, "--effort") != (tc.effort != "") {
				t.Errorf("the delegated claude ran %q, want no model pin and effort %q", argv, tc.effort)
			}
			run.Exit(0)
		})
	}
}

func TestDelegateRolesEditWalkBackAndRestoreTheDaemonsTable(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	roles := func(args ...string) string {
		t.Helper()
		got := s.Attn(append([]string{"delegate", "roles"}, args...)...)
		if got.Code != 0 {
			t.Fatalf("attn delegate roles %q exited %d: %s", args, got.Code, got.Stderr)
		}
		return got.Stdout
	}
	refused := func(want string, args ...string) {
		t.Helper()
		if got := s.Attn(append([]string{"delegate", "roles"}, args...)...); got.Code == 0 || !strings.Contains(got.Stderr, want) {
			t.Errorf("attn delegate roles %q exited %d with stderr %q, want a refusal containing %q", args, got.Code, got.Stderr, want)
		}
	}

	requireLines(t, "roles add", roles("add", "build", "--name", "Build", "--agent", "claude", "--model", "opus", "--effort", "high", "-m", "the user wants a builder"),
		"revision 1", "added role build (claude opus high)")
	requireLines(t, "roles add alternative", roles("add", "build/hard", "--when", "Concurrency", "--agent", "codex", "--model", "gpt-5.6-sol"),
		"build: added alternative hard (codex gpt-5.6-sol)")
	requireLines(t, "roles set", roles("set", "build", "--model", "sonnet"), "revision 3", "build: claude opus high → claude sonnet")
	refused("needs a default choice", "rm", "build/default")

	var exported protocol.DelegationPreferences
	s.Attn("delegate", "roles", "show", "--json").JSON(t, &exported)
	if exported.Revision != 3 || len(exported.Roles) != 1 {
		t.Fatalf("roles show --json after a refused edit = revision %d with %d roles, want revision 3 with build", exported.Revision, len(exported.Roles))
	}
	slices.Reverse(exported.Roles[0].Choices)
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	edited := s.Path("roles.json")
	if err := os.MkdirAll(filepath.Dir(edited), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(edited, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	requireLines(t, "roles apply", roles("apply", edited), "revision 4", "build: reordered alternatives")
	refused("the table changed after revision 3", "apply", edited)

	requireLines(t, "roles rollback", roles("rollback"), "revision 5 restores revision 3", "build: reordered alternatives")
	requireLines(t, "roles rollback", roles("rollback"), "revision 6 restores revision 2", "build: claude sonnet → claude opus high")
	requireLines(t, "roles rollback 4", roles("rollback", "4"), "revision 7 restores revision 4", "build: claude opus high → claude sonnet", "build: reordered alternatives")
	requireLines(t, "roles show", roles("show"), "revision 7", "build", "claude sonnet", "/hard", "when: Concurrency")
	requireLines(t, "roles history", roles("history", "--limit", "7"), "revision 7 (live)", "restores 4", "from the CLI", `"the user wants a builder"`, "added role build (claude opus high)")
}

func TestDelegateDesktopPlacesTheAgentOnTheNamedDesktop(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	notes := s.Path("notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	source := s.Spawn(app, fakeagent.Claude, notes)
	s.Launched(source)
	id := uuid.NewString()
	created := testworld.Request(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: app.SelectedProfile(), Name: protocol.Ptr("Ops")},
		protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == id })
	if !created.Success {
		t.Fatalf("creating the Ops desktop: %s", protocol.Deref(created.Error))
	}
	ops := created.Desktops[0].ID

	delegate := func(args ...string) testworld.Result {
		return s.Run(testworld.Invocation{Session: source, Args: append([]string{"delegate", "--brief", "Watch the deploy", "--cwd", notes, "--model", "opus", "--name", "watcher"}, args...)})
	}
	started := delegate("--desktop", "ops")
	if started.Code != 0 {
		t.Fatalf("attn delegate --desktop ops exited %d: %s", started.Code, started.Stderr)
	}
	var result struct {
		DesktopID string `json:"desktop_id"`
	}
	started.JSON(t, &result)
	if result.DesktopID != ops {
		t.Errorf("the delegate landed on desktop %q; want Ops %s", result.DesktopID, ops)
	}

	for _, tc := range []struct {
		ref  string
		code int
		want string
	}{
		{ref: "", code: 2, want: "--desktop needs a shortcut digit (1-9), a desktop name or a desktop id"},
		{ref: "nope", code: 1, want: `unknown desktop "nope"`},
	} {
		got := delegate("--desktop", tc.ref)
		if got.Code != tc.code || !strings.Contains(got.Stderr, tc.want) {
			t.Errorf("--desktop %q exited %d with stderr %q; want %d and %q", tc.ref, got.Code, got.Stderr, tc.code, tc.want)
		}
	}
}
