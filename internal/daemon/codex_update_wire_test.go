package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func codexUpdateNotifications(app *testworld.Peer) map[string]protocol.Notification {
	app.T.Helper()
	byTitle := map[string]protocol.Notification{}
	for _, n := range listNotifications(app).Notifications {
		if n.Kind == "codex_update_available" {
			byTitle[n.Title] = n
		}
	}
	return byTitle
}

func TestANewerCodexIsOfferedWithAnInstallActionUntilItIsInstalled(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	w.SetCodexVersion("0.162.1")
	w.SetCodexLatest("0.163.0")
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "", "0.162.1", "0.163.0", protocol.SharedCodexRestartNone)
	})
	install := []protocol.NotificationAction{{Kind: "install_codex_update", Label: "Install", TargetID: ""}}
	offered := func(n protocol.Notification) bool { return n.ReadAt == "" && slices.Equal(n.Actions, install) }
	resolved := func(n protocol.Notification) bool { return n.ReadAt != "" && len(n.Actions) == 0 }
	notes := codexUpdateNotifications(app)
	if len(notes) != 1 || !offered(notes["Codex 0.163.0 is available"]) {
		t.Fatalf("with Codex 0.162.1 installed and 0.163.0 out, the notifications hold %+v, want one unread for 0.163.0 with Install", notes)
	}

	w.SetCodexLatest("0.164.0")
	setSetting(t, app, "codex_shared_enabled", "false")
	setSetting(t, app, "codex_shared_enabled", "true")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "", "0.162.1", "0.164.0", protocol.SharedCodexRestartNone)
	})
	notes = codexUpdateNotifications(app)
	if len(notes) != 2 || !resolved(notes["Codex 0.163.0 is available"]) || !offered(notes["Codex 0.164.0 is available"]) {
		t.Fatalf("after Codex 0.164.0 came out, the notifications hold %+v, want 0.163.0 read without Install and 0.164.0 offered", notes)
	}

	w.SetCodexVersion("0.164.0")
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("shop")))
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "0.164.0", "0.164.0", "0.164.0", protocol.SharedCodexRestartNone)
	})
	notes = codexUpdateNotifications(app)
	if len(notes) != 2 || !resolved(notes["Codex 0.163.0 is available"]) || !resolved(notes["Codex 0.164.0 is available"]) {
		t.Fatalf("with Codex 0.164.0 installed, the notifications hold %+v, want both read without Install", notes)
	}
}
