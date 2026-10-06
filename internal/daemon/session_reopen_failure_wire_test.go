package daemon_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAReopenWhoseAgentCannotStartPutsTheWorkBackAsItWas(t *testing.T) {
	w := newWorld(t, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	repo := reopenRepoWithOrigin(t)

	closedPi := func(dir, reason string) protocol.SessionLedgerEntry {
		session := w.Spawn(app, fakeagent.Pi, dir)
		w.Launched(session)
		if _, err := cli.AgentClose(session, protocol.SessionID(session), reason); err != nil {
			t.Fatalf("%s closes itself: %v", session, err)
		}
		return awaitClosed(app, session)
	}
	plain := closedPi(w.Path("plain"), "brief delivered")
	worktree := reopenWorktree(t, repo, "feat/recreated")
	recreated := closedPi(worktree, "done for now")
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "worktree", "prune")

	defer w.RefusePiLaunches("pi could not start: the model provider is unreachable")()
	for _, c := range []struct {
		closed protocol.SessionLedgerEntry
		action protocol.SessionReopenAction
	}{
		{plain, protocol.SessionReopenActionReopen},
		{recreated, protocol.SessionReopenActionRecreateWorktreeAndReopen},
	} {
		if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: c.closed.ID, Action: string(c.action)}); err == nil || !strings.Contains(err.Error(), "provider is unreachable") {
			t.Errorf("%s: the reopen = %v, want it failing with pi's reason", c.closed.ID, err)
		}
		after := showSession(t, cli, string(c.closed.ID))
		if protocol.Deref(after.ClosedAt) != protocol.Deref(c.closed.ClosedAt) || protocol.Deref(after.ClosedBy) != protocol.Deref(c.closed.ClosedBy) ||
			protocol.Deref(after.CloseReason) != protocol.Deref(c.closed.CloseReason) {
			t.Errorf("%s: after the failed reopen the close reads %s by %s (%q), want it as it was: %s by %s (%q)", c.closed.ID,
				protocol.Deref(after.ClosedAt), protocol.Deref(after.ClosedBy), protocol.Deref(after.CloseReason),
				protocol.Deref(c.closed.ClosedAt), protocol.Deref(c.closed.ClosedBy), protocol.Deref(c.closed.CloseReason))
		}
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("the failed reopen left the worktree it recreated at %s (%v)", worktree, err)
	}
}
