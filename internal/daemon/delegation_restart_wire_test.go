package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADelegationInterruptedByARestartFinishesOnItsSessionInTheWorktreeItCreated(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	worktree := filepath.Join(filepath.Dir(repo), "shop--feature-interrupted")
	request := delegateCheckoutAt(repo, delegateNewWorktree("feature/interrupted", "main"))
	request.RequestID, request.Label = "interrupted", protocol.Ptr("interrupted")
	boot := w.HoldNextBoot()
	accepted, err := cli.StartDelegation(request)
	if err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(m protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range m.WorkspaceLayout.Panes {
			if protocol.Deref(pane.SessionID) == accepted.SessionID && pane.Status == protocol.WorkspaceLayoutPaneStatusReady {
				return true
			}
		}
		return false
	})

	w.stop()
	boot()
	w.start()
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
