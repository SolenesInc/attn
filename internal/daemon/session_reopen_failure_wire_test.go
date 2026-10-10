package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAFailedReopenKeepsItsAttemptAndRestoredWorktree(t *testing.T) {
	t.Setenv(fakeagent.PiCapabilitiesEnv, "state_reporting=false")
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

	allowLaunches := w.RefusePiLaunches("pi could not start: the model provider is unreachable")
	defer allowLaunches()
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
		if protocol.Deref(after.ClosedAt) != "" {
			t.Errorf("failed attempt is still closed: %+v", after)
		}
		if verdict := reopenVerdict(t, cli, string(c.closed.ID)); !verdict.Reopenable {
			t.Errorf("failed attempt is not retryable: %+v", verdict)
		}
		testworld.AwaitSession(app, string(c.closed.ID), func(session protocol.Session) bool {
			return protocol.Deref(session.StateReason) == "process_exited"
		})
		viewProfile(t, w, app.SelectedProfile()).paneOf(t, string(c.closed.ID))
	}
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		t.Fatalf("restored worktree %s was removed: %v", worktree, err)
	}
	w.restart()
	app, cli = w.App(), w.Client()
	awaitDriverAvailable(app, "pi")
	allowLaunches()
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: plain.ID}); err != nil {
		t.Fatal(err)
	}
	if run := w.Launched(string(plain.ID)); !run.Resumed {
		t.Errorf("CLI retry did not resume: %q", run.Argv)
	}
	terminal := w.App().Terminal(string(recreated.ID))
	executable := filepath.Join(w.Dir, "bin", "pi")
	disabled := executable + ".disabled"
	if err := os.Rename(executable, disabled); err != nil {
		t.Fatal(err)
	}
	failedRetry := reloadPi(app, string(recreated.ID))
	if err := os.Rename(disabled, executable); err != nil {
		t.Fatal(err)
	}
	if failedRetry.Success {
		t.Fatal("retry with unavailable executable succeeded")
	}
	testworld.AwaitSession(app, string(recreated.ID), func(session protocol.Session) bool {
		return protocol.Deref(session.StateReason) == "process_exited"
	})
	if verdict := reopenVerdict(t, cli, string(recreated.ID)); !verdict.Reopenable {
		t.Errorf("failed second attempt is not retryable: %+v", verdict)
	}
	if after := w.App().Terminal(string(recreated.ID)); after != terminal {
		t.Errorf("retry changed terminal %s to %s", terminal, after)
	}
	held := w.HoldNextBoot()
	defer held()
	reloaded := reloadPi(app, string(recreated.ID))
	if !reloaded.Success {
		t.Fatal(protocol.Deref(reloaded.Error))
	}
	found := false
	for _, session := range w.App().Initial.Sessions {
		if session.ID == recreated.ID {
			found = true
		}
		if session.ID == recreated.ID && protocol.Deref(session.StateReason) == "process_exited" {
			t.Errorf("successful retry still says stopped: %+v", session)
		}
	}
	if !found {
		t.Fatal("successful retry session missing")
	}
	held()
	resumed := w.Launched(string(recreated.ID))
	if !resumed.Resumed {
		t.Errorf("retry did not resume: %q", resumed.Argv)
	}
}
