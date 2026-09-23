package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func setupAgentDesktop(t *testing.T) (*Daemon, profiles.Desktop) {
	t.Helper()
	return setupAgentDesktopOn(t, NewForTesting(filepath.Join(t.TempDir(), "test.sock")))
}

func setupAgentDesktopOn(t *testing.T, d *Daemon) (*Daemon, profiles.Desktop) {
	t.Helper()
	injectTestSession(t, d, protocol.Session{ID: "session-1", Label: "agent", Directory: t.TempDir()})
	focusTestAgent(t, d, "session-1")
	placement, _, err := d.store.SessionPlacement("session-1")
	if err != nil {
		t.Fatal(err)
	}
	desktop, err := d.store.GetDesktop(placement.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	return d, desktop
}

func openBrowserFor(t *testing.T, d *Daemon, sessionID, url string) protocol.Response {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	msg := &protocol.OpenBrowserMessage{Cmd: protocol.CmdOpenBrowser, URL: url}
	if sessionID != "" {
		msg.SessionID = protocol.Ptr(sessionID)
	}
	go d.handleOpenBrowser(serverConn, msg)
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode open_browser response: %v", err)
	}
	return resp
}

func desktopTile(t *testing.T, d *Daemon, desktopID, tileID string) layouttree.TileLeaf {
	t.Helper()
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil {
		t.Fatal(err)
	}
	tile, found := tileLeafByID(desktop.Tree, tileID)
	if !found {
		t.Fatalf("desktop %s has no tile %s: %+v", desktopID, tileID, desktop.Tree)
	}
	return tile
}

func desktopTree(t *testing.T, d *Daemon, desktopID string) layouttree.Node {
	t.Helper()
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil {
		t.Fatal(err)
	}
	return desktop.Tree
}

func requireBrowserControlRequest(t *testing.T, host *wsClient) protocol.BrowserControlRequestMessage {
	t.Helper()
	for {
		outbound := requireOutbound(t, host, "no browser control request reached the host")
		var request protocol.BrowserControlRequestMessage
		if err := json.Unmarshal(outbound.payload, &request); err != nil {
			t.Fatal(err)
		}
		if request.Event == protocol.EventBrowserControlRequest {
			return request
		}
	}
}

func TestFocusingAnAgentMovesProfileDesktopAndPaneToIt(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	work := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "Work"}).Profile
	_, second, err := w.d.store.CreateDesktop(work.ID, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	w.agent("worker", work.ID)
	w.agent("waiting", work.ID)
	placeTestSession(t, w.d, "worker", second.ID)

	w.mustSend(client, map[string]any{"cmd": protocol.CmdDesktopFocusSession, "session_id": "worker"})
	profile, err := w.d.store.GetProfile(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	placement, _, _ := w.d.store.SessionPlacement("worker")
	desktop, err := w.d.store.GetDesktop(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if client.selectedProfile() != work.ID || profile.CurrentDesktopID != second.ID || desktop.ActivePaneID != placement.PaneID {
		t.Fatalf("after focusing worker: connection profile %q, current desktop %q, active pane %q; want %s, %s, %s",
			client.selectedProfile(), profile.CurrentDesktopID, desktop.ActivePaneID, work.ID, second.ID, placement.PaneID)
	}
	if recent, err := w.d.store.MostRecentlyUsedProfile(); err != nil || recent.ID != work.ID {
		t.Fatalf("most recently used profile = %+v, %v; want %s", recent, err, work.ID)
	}
	if got := w.d.currentAgentSession(); got != "worker" {
		t.Fatalf("current agent = %q, want worker", got)
	}

	w.mustSend(client, map[string]any{"cmd": protocol.CmdDesktopFocusSession, "session_id": "waiting"})
	if after, _ := w.d.store.GetProfile(work.ID); after.CurrentDesktopID != second.ID {
		t.Fatalf("focusing an unplaced agent moved the current desktop to %s", after.CurrentDesktopID)
	}
}
