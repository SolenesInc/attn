package main_test

import (
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
		return protocol.Deref(x.DispatcherSessionID) == source && protocol.Deref(x.Branch) == "feat/parser" && x.Directory == result.Directory
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
			argv := s.Launched(result.SessionID).Argv
			if slices.Contains(argv, "--model") || pinned(argv, "--effort") != tc.effort || slices.Contains(argv, "--effort") != (tc.effort != "") {
				t.Errorf("the delegated claude ran %q, want no model pin and effort %q", argv, tc.effort)
			}
		})
	}
}
