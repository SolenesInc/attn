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

func TestADelegateWhoseAgentOutlivesARestartStillFailsWhenItExitsBeforeItsFirstTurn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	cwd := s.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "outlives-restart", Cwd: cwd,
		Agent: protocol.Ptr("codex"), Label: protocol.Ptr("outlives-restart"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Say hello")},
	}
	boot := s.HoldNextBoot()
	if _, err := s.Client().StartDelegation(request); err != nil {
		t.Fatal(err)
	}
	s.AwaitHeldBoot()

	s.Stop()
	s.Start()
	const screen = `Error: Model "gpt-5.6-sol" is ambiguous across providers`
	s.ExitAtNextBoot(1, screen)
	boot()
	result, err := s.Client().Delegate(request)
	for _, want := range []string{"codex exited with code 1 before its first turn", "is ambiguous across providers"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("after the restart the delegation = %+v, %v; want the failure to say %q", result, err, want)
		}
	}
}

func TestAHandoverInterruptedByARestartFinishesOnTheSuccessorThatOutlivedIt(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	cli := s.Client()
	cwd := s.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	predecessor, err := cli.Delegate(protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "first", Cwd: cwd, Agent: protocol.Ptr("codex"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Investigate the tracked task.")},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Launched(string(predecessor.SessionID))
	handover := protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "handover", Cwd: cwd, Agent: protocol.Ptr("codex"),
		Assignment: protocol.DelegateAssignment{
			Kind: protocol.DelegateAssignmentKindSeed, SeedID: protocol.Ptr(predecessor.SeedID),
			Handover: &protocol.DelegateHandover{Note: protocol.Ptr("Continue from the failing test.")},
		},
	}
	boot := s.HoldNextBoot()
	accepted, err := cli.StartDelegation(handover)
	if err != nil {
		t.Fatal(err)
	}
	s.AwaitHeldBoot()

	s.Stop()
	s.Start()
	boot()
	successor := s.Launched(string(accepted.SessionID))
	result, err := s.Client().Delegate(handover)
	if err != nil || result.SessionID != accepted.SessionID {
		t.Fatalf("after the restart the handover = %+v, %v; want it finished on %s", result, err, accepted.SessionID)
	}
	if prompt := successor.Prompted(); !strings.Contains(prompt, "attn seed show "+predecessor.SeedID) {
		t.Errorf("the successor that outlived the restart was prompted %q, want a pointer to its seed", prompt)
	}
	shown, err := s.Client().SeedShow("", predecessor.SeedID)
	if err != nil {
		t.Fatal(err)
	}
	handoffs := 0
	for _, note := range shown.Notes {
		if note.Kind == "handoff" {
			handoffs++
		}
	}
	if protocol.Deref(protocol.Deref(shown.Seed.Tender).SessionID) != accepted.SessionID || handoffs != 1 {
		t.Errorf("after the restart the seed is tended by %q with %d handoff notes, want %s with one", protocol.Deref(protocol.Deref(shown.Seed.Tender).SessionID), handoffs, accepted.SessionID)
	}
	if sessions := s.App().Initial.Sessions; len(sessions) != 2 {
		t.Errorf("after the restart there are %d sessions, want the predecessor and its one successor", len(sessions))
	}
}

func TestAfterACrashADelegationNeverAdoptsAWorktreeItCannotProveItCreated(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, crashAt string
		checkout      protocol.DelegateCheckout
	}{
		{name: "a worktree that appeared before attn recorded creating it", crashAt: "delegation-worktree-journaled",
			checkout: protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindExistingBranchWorktree, Branch: "feature/discount"}},
		{name: "a worktree that replaced the one attn created", crashAt: "delegation-worktree-owned",
			checkout: protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindNewWorktree, Branch: "feature/discount", From: protocol.Ptr("main")}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
			repo := s.Path("shop")
			gitRepo(t, repo)
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = repo
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
				}
			}
			worktree := filepath.Join(filepath.Dir(repo), "shop--feature-discount")
			request := protocol.DelegateMessage{
				Cmd: protocol.CmdDelegate, RequestID: "discount", Cwd: repo, Agent: protocol.Ptr("codex"), Checkout: &row.checkout,
				Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Add a discount field")},
			}
			if row.checkout.Kind == protocol.DelegateCheckoutKindExistingBranchWorktree {
				git("branch", "feature/discount")
			}
			s.StartCrashingAt(row.crashAt)
			_, _ = s.Client().StartDelegation(request)
			s.AwaitCrash()

			if _, err := os.Stat(worktree); err == nil {
				git("worktree", "remove", "--force", worktree)
				git("branch", "-D", "feature/discount")
				git("worktree", "add", "-q", "-b", "feature/discount", worktree)
			} else {
				git("worktree", "add", "-q", worktree, "feature/discount")
			}
			work := filepath.Join(worktree, "mine.txt")
			if err := os.WriteFile(work, []byte("the user's work"), 0o644); err != nil {
				t.Fatal(err)
			}

			s.Start()
			if _, err := s.Client().Delegate(request); err == nil || !strings.Contains(err.Error(), "left untouched") {
				t.Errorf("after the crash the delegation ended with %v, want it failed leaving the worktree untouched", err)
			}
			if got, err := os.ReadFile(work); err != nil || string(got) != "the user's work" {
				t.Errorf("the worktree at %s now holds %q (%v), want the user's work intact", worktree, got, err)
			}
			if sessions := s.App().Initial.Sessions; len(sessions) != 0 {
				t.Errorf("after the refused delegation %d sessions run, want none", len(sessions))
			}
		})
	}
}
