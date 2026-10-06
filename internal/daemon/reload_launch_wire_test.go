package daemon_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func compactWindow(run *fakeagent.Run) string {
	for _, pair := range run.Env {
		if value, ok := strings.CutPrefix(pair, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="); ok {
			return value
		}
	}
	return ""
}

func TestAReloadRelaunchesWithTheContextWindowCapTheSessionIsDue(t *testing.T) {
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "")
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	chief := w.Spawn(app, fakeagent.Claude, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	worker := w.Spawn(app, fakeagent.Claude, w.Path("worker"))
	pinned := w.Spawn(app, fakeagent.Claude, w.Path("pinned"))
	for _, session := range []string{chief, worker, pinned} {
		w.Launched(session)
	}
	reloaded := func(session string) string {
		t.Helper()
		launchIntentReload(t, app, session)
		return compactWindow(w.Launched(session))
	}

	if got := reloaded(chief); got != "128000" {
		t.Errorf("with no chief cap configured the reloaded chief runs with window %q, want the default 128000", got)
	}
	setSetting(t, app, "chief_context_window_cap", "160000")
	if got := reloaded(worker); got != "" {
		t.Errorf("with only a chief cap configured the reloaded worker runs with window %q, want none", got)
	}
	setSetting(t, app, "default_context_window_cap_claude", "800000")
	pin := testworld.Request(app, protocol.SetSessionContextWindowCapMessage{Cmd: protocol.CmdSetSessionContextWindowCap, SessionID: protocol.SessionID(pinned), Cap: 300000},
		protocol.EventSessionContextWindowCapResult, func(r protocol.SessionContextWindowCapResultMessage) bool { return string(r.SessionID) == pinned })
	if !pin.Success {
		t.Fatalf("pin %s to 300000: %s", pinned, protocol.Deref(pin.Error))
	}
	for session, want := range map[string]string{chief: "160000", worker: "800000", pinned: "300000"} {
		if got := reloaded(session); got != want {
			t.Errorf("the reloaded %s runs with window %q, want %s", session, got, want)
		}
	}
}

func TestAReloadWhoseAgentCannotStartShowsTheSessionExited(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd)
	w.Launched(session)
	if err := os.RemoveAll(cwd); err != nil {
		t.Fatal(err)
	}
	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
	if reloaded.Success {
		t.Fatal("a reload into a deleted directory succeeded")
	}
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
}

func TestTwoReloadsAtOnceLeaveOneAgentRunning(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, other := w.App(), w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)

	boot := w.HoldNextBoot()
	reload := protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30}
	app.Send(reload)
	other.Send(reload)
	for _, peer := range []*testworld.Peer{app, other} {
		result := testworld.Await(peer, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
		if !result.Success {
			t.Errorf("a concurrent reload failed: %s", protocol.Deref(result.Error))
		}
	}
	boot()
	last := w.Launched(session)
	app.TypeLine(session, "run the tests")
	if got := last.Prompted(); got != "run the tests" {
		t.Errorf("the agent the second reload started received %q, want the user's prompt", got)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	for _, e := range app.Received() {
		if e.Event == protocol.EventSessionExited && string(protocol.Deref(e.SessionID)) == session {
			t.Error("the concurrent reloads showed the session exited")
		}
	}
}
