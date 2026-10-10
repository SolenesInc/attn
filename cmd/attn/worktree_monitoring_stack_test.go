package main_test

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAutomationCheckoutRecoveredAfterACrashKeepsItsRepositoryMonitored(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_MOCK_GH_HOST=github.test", "ATTN_MOCK_GH_TOKEN=test-token")
	repo := s.Path("shop")
	gitRepo(t, repo)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("remote", "add", "origin", "git@github.test:acme/shop.git")
	head := git("rev-parse", "HEAD")
	s.StartCrashingAt("automation-worktree-prepared")
	cli := s.Client()
	applied, err := cli.AutomationApply(fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Review locally
trigger: {type: manual}
prompt: Review this pull request.
launch: {driver: claude, model: sonnet}
location:
  type: repository_worktree
  repository_sources:
    default: {type: managed_cache}
    overrides:
      github.test/acme/shop: {type: local_clone, path: %q}
`, repo))
	if err != nil {
		t.Fatal(err)
	}
	id := applied.Definition.ID
	input := fmt.Sprintf(`{"provider":"github","host":"github.test","owner":"acme","repository":"shop","number":42,"url":"https://github.test/acme/shop/pull/42","state":"open","draft":false,"head_sha":%q}`, head)
	_, _ = cli.AutomationRun(id, "review", input)
	s.AwaitCrash()
	checkouts, err := filepath.Glob(filepath.Join(s.Dir, "automation", "worktrees", "*", "shop"))
	if err != nil || len(checkouts) != 1 {
		t.Fatalf("checkouts after the crash = %v, %v, want the created checkout", checkouts, err)
	}

	s.Start()
	app, cli := s.App(), s.Client()
	var recovered protocol.AutomationRunSummary
	for {
		runs, err := cli.AutomationRuns(id)
		if err != nil || len(runs.Runs) != 1 {
			t.Fatalf("recovered runs = %+v, %v, want the original run", runs, err)
		}
		recovered = runs.Runs[0]
		if recovered.State == "delivered" {
			break
		}
		if recovered.State == "failed" {
			t.Fatalf("recovered run failed: %s", protocol.Deref(recovered.LastError))
		}
		testworld.Await(app, protocol.EventAutomationsChanged, func(m protocol.AutomationsChangedMessage) bool {
			return slices.Contains(m.DefinitionIds, id)
		})
	}
	session := protocol.Deref(recovered.SessionID)
	s.Launched(string(session))
	if err := cli.Unregister(session); err != nil {
		t.Fatal(err)
	}
	cleaned, err := cli.AutomationCleanup(id)
	if err != nil || !slices.Equal(cleaned.Cleaned, []string{recovered.ID}) {
		t.Fatalf("cleanup = %+v, %v, want the recovered checkout removed", cleaned, err)
	}
	s.Stop()
	s.Start()
	app, cli = s.App(), s.Client()
	external := s.Path("external")
	git("worktree", "add", "-b", "external", external)
	if refresh, err := cli.WorktreeRefresh(); err != nil || !refresh.Queued {
		t.Fatalf("refresh = %+v, %v", refresh, err)
	}
	observed := testworld.Await(app, protocol.EventWorktreeStateChanged, func(m protocol.WebSocketEvent) bool {
		return len(m.Worktrees) == 1 && m.Worktrees[0].Path == external
	}).Worktrees[0]
	if observed.MainRepo != repo || observed.Branch != "external" {
		t.Errorf("external checkout = %+v, want discovery in the recovered automation's repository", observed)
	}
}
