package daemon_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var shellHarness = fakeagent.Harness(protocol.SessionAgentShell)

func queriedSession(t *testing.T, cli *client.Client, id string) protocol.Session {
	t.Helper()
	listed, err := cli.Query("")
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	for _, s := range listed {
		if string(s.ID) == id {
			return s
		}
	}
	t.Fatalf("session %s is not listed", id)
	return protocol.Session{}
}

func exitShells(app *testworld.Peer, ids ...string) {
	app.T.Helper()
	for _, id := range ids {
		app.TypeLine(id, "exit")
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == id })
	}
}

func closeFromApp(app *testworld.Peer, sessionID string) protocol.SessionCloseResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(sessionID)},
		protocol.EventSessionCloseResult, func(r protocol.SessionCloseResultMessage) bool { return string(r.SessionID) == sessionID })
}

func placedPane(t *testing.T, w *world, sessionID string) (protocol.Desktop, string) {
	t.Helper()
	for _, desktop := range w.App().Initial.Desktops {
		for _, pane := range desktop.Panes {
			if string(pane.SessionID) == sessionID {
				return desktop, pane.PaneID
			}
		}
	}
	t.Fatalf("session %s is placed on no desktop", sessionID)
	return protocol.Desktop{}, ""
}

func focusAgent(t *testing.T, w *world, app *testworld.Peer, sessionID string) {
	t.Helper()
	desktop, pane := placedPane(t, w, sessionID)
	switched := testworld.Request(app, protocol.DesktopSetCurrentMessage{
		Cmd: protocol.CmdDesktopSetCurrent, ProfileID: desktop.ProfileID, DesktopID: desktop.ID, RequestID: "switch-" + sessionID,
	}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "switch-"+sessionID })
	if !switched.Success {
		t.Fatalf("switching to the desktop of %s: %s", sessionID, protocol.Deref(switched.Error))
	}
	requestID := "focus-" + sessionID
	focused := testworld.Request(app, protocol.DesktopSetActivePaneMessage{
		Cmd: protocol.CmdDesktopSetActivePane, DesktopID: desktop.ID, PaneID: pane, RequestID: requestID,
	}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == requestID })
	if !focused.Success {
		t.Fatalf("focusing %s: %s", sessionID, protocol.Deref(focused.Error))
	}
}

type layoutNode struct {
	Type          string       `json:"type"`
	PaneID        string       `json:"pane_id"`
	TileID        string       `json:"tile_id"`
	TileKind      string       `json:"tile_kind"`
	TileParams    string       `json:"tile_params"`
	TileSessionID string       `json:"tile_session_id"`
	SplitID       string       `json:"split_id"`
	Direction     string       `json:"direction"`
	Ratio         float64      `json:"ratio"`
	RatioLocked   bool         `json:"ratio_locked"`
	RatioMode     string       `json:"ratio_mode"`
	Children      []layoutNode `json:"children"`
}

func desktopTree(t *testing.T, desktop protocol.Desktop) layoutNode {
	t.Helper()
	var root layoutNode
	if err := json.Unmarshal([]byte(desktop.TreeJson), &root); err != nil {
		t.Fatalf("the tree of desktop %s is not a node tree: %v: %s", desktop.ID, err, desktop.TreeJson)
	}
	return root
}

func (n layoutNode) leafIDs() []string {
	switch n.Type {
	case "pane":
		return []string{n.PaneID}
	case "tile":
		return []string{n.TileID}
	}
	var ids []string
	for _, child := range n.Children {
		ids = append(ids, child.leafIDs()...)
	}
	return ids
}

func (n layoutNode) tiles() map[string]layoutNode {
	found := map[string]layoutNode{}
	if n.Type == "tile" {
		found[n.TileID] = n
	}
	for _, child := range n.Children {
		for id, tile := range child.tiles() {
			found[id] = tile
		}
	}
	return found
}

func (n layoutNode) split(id string) (layoutNode, bool) {
	if n.Type == "split" && n.SplitID == id {
		return n, true
	}
	for _, child := range n.Children {
		if found, ok := child.split(id); ok {
			return found, true
		}
	}
	return layoutNode{}, false
}

func (n layoutNode) splitHolding(leafID string) (split layoutNode, index int, ok bool) {
	for i, child := range n.Children {
		if slices.Equal(child.leafIDs(), []string{leafID}) {
			return n, i, true
		}
		if found, at, ok := child.splitHolding(leafID); ok {
			return found, at, true
		}
	}
	return layoutNode{}, 0, false
}

func awaitDesktop(app *testworld.Peer, desktopID string, match func(protocol.Desktop) bool) protocol.Desktop {
	app.T.Helper()
	var found protocol.Desktop
	testworld.Await(app, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
		for _, desktop := range e.Desktops {
			if desktop.ID == desktopID && match(desktop) {
				found = desktop
				return true
			}
		}
		return false
	})
	return found
}
