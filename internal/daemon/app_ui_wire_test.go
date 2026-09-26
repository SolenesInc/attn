package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheAppRegistryShowsTheServingVersionsViews(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	approvals := appTileView("approvals", "Pending approvals")
	approvals.Params = &appbuild.ViewParams{Label: "Ticket id", Placeholder: "t-1234"}
	older := applyAppWithViews(t, cli, "reviewer", "reviews approvals", approvals)
	newer := applyAppWithViews(t, cli, "reviewer", "reviews approvals", approvals, appTileView("history", "History"))

	app := w.App()
	entry := appRegistryEntryOf(t, app.Initial.Apps, "reviewer")
	if !entry.Enabled || protocol.Deref(entry.VersionID) != newer.VersionID || protocol.Deref(entry.ContentHash) != newer.ContentHash {
		t.Fatalf("registry entry = %+v, want enabled at version %d with hash %s", entry, newer.VersionID, newer.ContentHash)
	}
	if protocol.Deref(entry.Description) != "reviews approvals" {
		t.Errorf("description = %v", entry.Description)
	}
	if got := appViewNames(entry); got != "approvals history" {
		t.Fatalf("views = %q, want both the serving version declares", got)
	}
	if view := entry.Views[0]; view.Kind != appbuild.ViewKindTile || view.Title != "Pending approvals" ||
		protocol.Deref(view.ParamsLabel) != "Ticket id" || protocol.Deref(view.ParamsPlaceholder) != "t-1234" {
		t.Errorf("the approvals view = %+v, want a tile titled Pending approvals with its params field", view)
	}

	if _, err := cli.AppRollback("reviewer", older.VersionID); err != nil {
		t.Fatalf("roll back: %v", err)
	}
	rolled := awaitAppRegistryEntry(app, "reviewer", func(e protocol.AppRegistryEntry) bool {
		return protocol.Deref(e.VersionID) == older.VersionID
	})
	if got := appViewNames(rolled); got != "approvals" || protocol.Deref(rolled.ContentHash) != older.ContentHash {
		t.Errorf("after the rollback the registry offers %q at hash %v, want just approvals at %s", got, rolled.ContentHash, older.ContentHash)
	}

	if _, err := cli.AppSetEnabled("reviewer", false); err != nil {
		t.Fatalf("disable reviewer: %v", err)
	}
	awaitAppRegistryEntry(app, "reviewer", func(e protocol.AppRegistryEntry) bool { return !e.Enabled })
}

func TestAViewCrashIsRecordedAgainstTheVersionThatServedIt(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	reviewer := applyAppWithViews(t, cli, "reviewer", "", appTileView("approvals", "Pending approvals"))
	planner := applyAppWithViews(t, cli, "planner", "", appTileView("plan", "Plan"))
	app := w.App()

	reportAppViewCrash(app, reviewer.VersionID, "TypeError: board is undefined\n    at Approvals (approvals.js:1:199)")
	reportAppViewCrash(app, planner.VersionID, "TypeError: from a version of another app")
	flushAppPeer(app)

	status := appStatus(t, cli, "reviewer")
	if status.Invocations != 1 || len(status.Recent) != 1 {
		t.Fatalf("invocations = %d %+v, want only the crash stamped with reviewer's own version", status.Invocations, status.Recent)
	}
	crash := status.Recent[0]
	if crash.VersionID != reviewer.VersionID || crash.Handler != apps.ViewLabel("approvals") || crash.Status != "error" ||
		protocol.Deref(crash.EventSubject) != "tile-7" || !strings.Contains(protocol.Deref(crash.Error), "TypeError: board is undefined") {
		t.Errorf("the crash = %+v, want an error of the approvals view at version %d from tile-7", crash, reviewer.VersionID)
	}
	logs, err := cli.AppLogs("reviewer", 20)
	if err != nil {
		t.Fatalf("app logs reviewer: %v", err)
	}
	requireAppLogLines(t, logs.Lines, fmt.Sprintf("view approvals crashed while rendering (version %d)", reviewer.VersionID),
		"TypeError: board is undefined", "at Approvals (approvals.js:1:199)")

	reportAppViewCrash(app, reviewer.VersionID, "TypeError: boom\n"+strings.Repeat("    at Component (bundle.js:1:1)\n", 4000))
	flushAppPeer(app)
	huge := appStatus(t, cli, "reviewer").Recent[0]
	if text := protocol.Deref(huge.Error); len(text) > 32*1024+128 || !strings.HasPrefix(text, "TypeError: boom") || !strings.Contains(text, "truncated") {
		t.Errorf("the oversized crash was recorded as %d bytes ending %q, want it truncated past 32 KiB and marked", len(text), text[max(0, len(text)-120):])
	}
}

func applyAppWithViews(t *testing.T, cli *client.Client, name, description string, views ...appbuild.View) *protocol.AppApplyResult {
	t.Helper()
	artifacts := make([]appbuild.ViewArtifact, 0, len(views))
	for _, v := range views {
		artifacts = append(artifacts, appbuild.ViewArtifact{Name: v.Name, Content: []byte("export default function " + v.Name + "() {}\n")})
	}
	declaration := appManifestDeclaration(t, appbuild.Manifest{Name: name, Description: description, Views: views})
	return applyAppVersion(t, cli, name, declaration, fmt.Sprintf("export default {} // %d views", len(views)), artifacts...)
}

func reportAppViewCrash(app *testworld.Peer, versionID int, text string) {
	app.Send(protocol.AppViewCrashMessage{Cmd: protocol.CmdAppViewCrash, App: "reviewer", View: "approvals", VersionID: versionID, TileID: "tile-7", Error: text})
}

func flushAppPeer(app *testworld.Peer) {
	app.T.Helper()
	requestAppCommand(app, "flush-marker", "flush", "")
}

func appRegistryEntryOf(t *testing.T, entries []protocol.AppRegistryEntry, name string) protocol.AppRegistryEntry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("app %q is not in the registry (%d entries)", name, len(entries))
	return protocol.AppRegistryEntry{}
}

func awaitAppRegistryEntry(app *testworld.Peer, name string, match func(protocol.AppRegistryEntry) bool) protocol.AppRegistryEntry {
	app.T.Helper()
	var found protocol.AppRegistryEntry
	testworld.Await(app, protocol.EventAppsUpdated, func(m protocol.AppsUpdatedMessage) bool {
		for _, e := range m.Apps {
			if e.Name == name && match(e) {
				found = e
				return true
			}
		}
		return false
	})
	return found
}

func appViewNames(entry protocol.AppRegistryEntry) string {
	names := make([]string, 0, len(entry.Views))
	for _, v := range entry.Views {
		names = append(names, v.Name)
	}
	return strings.Join(names, " ")
}

func requireAppLogLines(t *testing.T, lines []string, want ...string) {
	t.Helper()
	whole := strings.Join(lines, "\n")
	for _, text := range want {
		if !strings.Contains(whole, text) {
			t.Errorf("the app log does not carry %q:\n%s", text, whole)
		}
	}
}
