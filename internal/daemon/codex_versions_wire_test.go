package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func awaitSharedCodex(app *testworld.Peer, match func(*protocol.SharedCodexState) bool) {
	app.T.Helper()
	testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool { return match(m.SharedCodex) })
}

func sharedCodexVersions(s *protocol.SharedCodexState, running, installed, latest string, restart protocol.SharedCodexRestart) bool {
	return s != nil && protocol.Deref(s.RunningVersion) == running && protocol.Deref(s.InstalledVersion) == installed &&
		protocol.Deref(s.LatestVersion) == latest && s.Restart == restart
}

func TestSharedCodexReportsTheVersionItRunsWhatIsInstalledAndTheNewestRelease(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	w.SetCodexVersion("0.162.1")
	w.SetCodexLatest("0.163.0")
	app := w.App()
	if app.Initial.SharedCodex != nil {
		t.Fatalf("with Shared Codex off the app was sent %+v", *app.Initial.SharedCodex)
	}
	setSetting(t, app, "codex_shared_enabled", "true")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "", "0.162.1", "0.163.0", protocol.SharedCodexRestartNone)
	})

	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	w.Launched(session)
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "0.162.1", "0.162.1", "0.163.0", protocol.SharedCodexRestartNone)
	})
	w.SetCodexVersion("0.163.0")
	second := w.Spawn(app, fakeagent.Codex, w.Path("cart"))
	w.Launched(second)
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "0.162.1", "0.163.0", "0.163.0", protocol.SharedCodexRestartPending)
	})
	if s := w.App().Initial.SharedCodex; !sharedCodexVersions(s, "0.162.1", "0.163.0", "0.163.0", protocol.SharedCodexRestartPending) {
		t.Errorf("a new connection was sent %+v, want the restart pending", s)
	}

	home := app.SelectedProfile()
	side := createProfile(app, "side")
	if s := w.AppOn(side.ID).Initial.SharedCodex; !sharedCodexVersions(s, "", "0.163.0", "0.163.0", protocol.SharedCodexRestartNone) {
		t.Errorf("a new connection to a profile with no Codex running was sent %+v", s)
	}
	selectProfile(app, side.ID)
	testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
		return m.ProfileID == side.ID && sharedCodexVersions(m.SharedCodex, "", "0.163.0", "0.163.0", protocol.SharedCodexRestartNone)
	})
	selectProfile(app, home)
	testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
		return m.ProfileID == home && sharedCodexVersions(m.SharedCodex, "0.162.1", "0.163.0", "0.163.0", protocol.SharedCodexRestartPending)
	})

	setSetting(t, app, "codex_shared_enabled", "false")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool { return s == nil })
}

func TestATerminalStillOnAnOlderCodexThanItsAppServerIsReported(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	w.SetCodexVersion("0.162.1")
	w.SetCodexLatest("0.162.1")
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	terminal := w.Terminal(session)
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "0.162.1", "0.162.1", "0.162.1", protocol.SharedCodexRestartNone) && len(s.StaleSessions) == 0
	})

	w.SetCodexVersion("0.163.0")
	codex.CrashAppServer()
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("codex received %q after its app-server came back", got)
	}
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return s != nil && protocol.Deref(s.RunningVersion) == "0.163.0" && slices.Equal(s.StaleSessions, []protocol.SessionID{protocol.SessionID(session)})
	})

	codex.Reply("Added. <!-- attn:state=idle -->")
	moveOn(t, app, codex, terminal, "/new", "add a tax field")
	moved := sessionShownIn(t, w, app, terminal)
	if moved == session {
		t.Fatalf("terminal %s still shows %s after /new and a prompt", terminal, session)
	}
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return s != nil && slices.Equal(s.StaleSessions, []protocol.SessionID{protocol.SessionID(moved)})
	})
}

func TestSharedCodexRunsTheConfiguredCodexForItsAppServerAndItsTerminals(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	w.SetCodexVersion("0.162.1")
	app := w.App()
	configured := w.InstallCodexAt("configured-codex", "0.170.0")
	setSetting(t, app, "codex_shared_enabled", "true")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return s != nil && protocol.Deref(s.InstalledVersion) == "0.162.1"
	})
	setSetting(t, app, "codex_executable", configured)
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return s != nil && protocol.Deref(s.InstalledVersion) == "0.170.0"
	})
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Executable, m.CodexExecutable = protocol.Ptr(configured), protocol.Ptr(configured)
	})
	if got := w.Launched(session).Argv[0]; got != configured {
		t.Errorf("the terminal ran %s, want the configured %s", got, configured)
	}
	if got := w.CodexServer().Argv()[0]; got != configured {
		t.Errorf("the app-server ran %s, want the configured %s", got, configured)
	}
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return s != nil && protocol.Deref(s.RunningVersion) == "0.170.0" && protocol.Deref(s.InstalledVersion) == "0.170.0"
	})
}
