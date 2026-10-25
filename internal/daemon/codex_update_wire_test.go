package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func codexUpdateOffers(app *testworld.Peer) []protocol.Notification {
	app.T.Helper()
	var offers []protocol.Notification
	for _, n := range listNotifications(app).Notifications {
		if n.Kind == "codex_update_available" {
			offers = append(offers, n)
		}
	}
	return offers
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
	offers := codexUpdateOffers(app)
	install := []protocol.NotificationAction{{Kind: "install_codex_update", Label: "Install", TargetID: ""}}
	if len(offers) != 1 || offers[0].Title != "Codex 0.163.0 is available" || !slices.Equal(offers[0].Actions, install) {
		t.Fatalf("with Codex 0.162.1 installed and 0.163.0 out, the notifications offer %+v, want one for 0.163.0 with Install", offers)
	}

	w.SetCodexLatest("0.164.0")
	setSetting(t, app, "codex_shared_enabled", "false")
	setSetting(t, app, "codex_shared_enabled", "true")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "", "0.162.1", "0.164.0", protocol.SharedCodexRestartNone)
	})
	if offers := codexUpdateOffers(app); len(offers) != 1 || offers[0].Title != "Codex 0.164.0 is available" {
		t.Fatalf("after Codex 0.164.0 came out, the notifications offer %+v, want only 0.164.0", offers)
	}

	w.SetCodexVersion("0.164.0")
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("shop")))
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return sharedCodexVersions(s, "0.164.0", "0.164.0", "0.164.0", protocol.SharedCodexRestartNone)
	})
	if offers := codexUpdateOffers(app); len(offers) != 0 {
		t.Fatalf("with Codex 0.164.0 installed, the notifications still offer %+v", offers)
	}
}
