package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func repoWithOwnHead(t *testing.T, dir string) string {
	t.Helper()
	gitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "name.txt"), []byte(dir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "name.txt"},
		{"-c", "user.name=attn", "-c", "user.email=attn@example.invalid", "commit", "-q", "-m", "name " + filepath.Base(dir)},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(head))
}

func TestACheckReportsCommitResolvesInTheRepositoryTheCallersGitEnvironmentSelects(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	mark := app.EventCount()
	requireStdout(t, s.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
	testworld.AwaitSince(app, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["agent_tools_enabled"] == "true"
	})
	shop, a, b := s.Path("shop"), s.Path("a"), s.Path("b")
	shopHead, aHead, bHead := repoWithOwnHead(t, shop), repoWithOwnHead(t, a), repoWithOwnHead(t, b)
	seed := plant(t, s, "Commit resolution").ID
	requireStdout(t, s.Attn("seed", "check", "add", seed, "C1", "-m", "It resolves"))
	reported := func() string {
		t.Helper()
		for _, check := range showSeed(t, s, "", seed).Seed.Checks {
			if check.ID == "C1" && check.Report != nil {
				return protocol.Deref(check.Report.Commit)
			}
		}
		return ""
	}

	for _, run := range []struct {
		name string
		env  []string
		want string
	}{
		{"no Git variables", nil, aHead},
		{"GIT_DIR and GIT_WORK_TREE naming b", []string{"GIT_DIR=" + filepath.Join(b, ".git"), "GIT_WORK_TREE=" + b}, bHead},
		{"a relative GIT_DIR naming b", []string{"GIT_DIR=../b/.git"}, bHead},
	} {
		ran := s.Run(testworld.Invocation{Dir: a, Env: run.env, Args: []string{"seed", "check", "report", seed, "C1", "-m", run.name, "--evidence", "https://example.test/run", "--commit", "HEAD"}})
		if ran.Code != 0 {
			t.Fatalf("attn seed check report --commit HEAD in %s with %s exited %d: %s", a, run.name, ran.Code, ran.Stderr)
		}
		if got := reported(); got != run.want {
			t.Errorf("C1's report names commit %q after the CLI in %s with %s named HEAD, want %s", got, a, run.name, run.want)
		}
	}

	session := s.Spawn(app, fakeagent.Claude, shop)
	tools := claudeToolEndpoint(t, s.LaunchedCarrying(app.Terminal(session)))
	tools.structured(t, "seed_check_report", map[string]any{"seed": seed, "id": "C1", "summary": "By the tool", "evidence": []any{"https://example.test/run"}, "commit": "HEAD"}, &struct{}{})
	if got := reported(); got != shopHead {
		t.Errorf("C1's report names commit %q after a tool call named HEAD, want %s from the session's repository %s", got, shopHead, shop)
	}
}
