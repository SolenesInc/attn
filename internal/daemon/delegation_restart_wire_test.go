package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestADelegationInterruptedByARestartFinishesOnItsSessionInTheWorktreeItCreated(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	repo := newRepo(t, "shop")
	worktree := filepath.Join(filepath.Dir(repo), "shop--feature-interrupted")
	request := delegateCheckoutAt(repo, delegateNewWorktree("feature/interrupted", "main"))
	request.RequestID, request.Label = "interrupted", protocol.Ptr("interrupted")
	boot := w.HoldNextBoot()
	accepted, err := w.Client().StartDelegation(request)
	if err != nil {
		t.Fatal(err)
	}
	w.AwaitHeldBoot()

	w.stop()
	boot()
	w.start()
	w.Launched(accepted.SessionID)
	result, err := w.Client().Delegate(request)
	if err != nil || result.SessionID != accepted.SessionID || result.Directory != worktree || result.Checkout != "created" {
		t.Fatalf("after the restart the delegation = %+v, %v; want it finished on %s in the worktree it created at %s", result, err, accepted.SessionID, worktree)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Errorf("the restart took away the worktree the delegation created: %v", err)
	}
	if sessions := w.App().Initial.Sessions; len(sessions) != 1 || sessions[0].ID != accepted.SessionID {
		t.Errorf("after the restart the sessions are %+v, want only the delegate %s", sessions, accepted.SessionID)
	}
}
