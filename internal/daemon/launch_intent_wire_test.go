package daemon_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARelaunchedSessionKeepsTheChoicesItWasSpawnedWith(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	installed, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("find the installed claude: %v", err)
	}
	if err := os.MkdirAll(w.Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(installed), w.Path("pinned-bin")); err != nil {
		t.Fatal(err)
	}
	executable := w.Path("pinned-bin", "claude")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.YoloMode = protocol.Ptr(true)
		m.Executable = protocol.Ptr(executable)
		m.Model = protocol.Ptr("claude-sonnet-5")
		m.Effort = protocol.Ptr("high")
	})
	check := func(when string, run *fakeagent.Run) {
		t.Helper()
		model, _ := flagValue(run.Argv, "--model")
		effort, _ := flagValue(run.Argv, "--effort")
		if run.Argv[0] != executable || !slices.Contains(run.Argv, "--dangerously-skip-permissions") || model != "claude-sonnet-5" || effort != "high" {
			t.Errorf("%s claude ran as %q, want %s skipping permissions with model claude-sonnet-5 and effort high", when, run.Argv, executable)
		}
	}
	check("at spawn", w.Launched(session))

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	launchIntentReload(t, app, session)
	reloaded := w.Launched(session)
	check("reloaded after it exited", reloaded)
	app.TypeLine(session, "add a discount field to checkout")
	reloaded.Prompted()
	reloaded.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "classifier_verdict"
	})

	w.restart()
	app = w.App()
	launchIntentReload(t, app, session)
	check("reloaded after a daemon restart", w.Launched(session))
}

func launchIntentReload(t *testing.T, app *testworld.Peer, session string) {
	t.Helper()
	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: session, Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return r.ID == session })
	if !reloaded.Success {
		t.Fatalf("reload %s: %s", session, protocol.Deref(reloaded.Error))
	}
}
