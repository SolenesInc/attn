package daemon_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClosingTilesWalksTheDesktopFocusHistory(t *testing.T) {
	for _, row := range []struct {
		name   string
		visits []string
		closes []string
		wants  []string
	}{
		{"previous tile", []string{"a", "b", "c"}, []string{"c", "b"}, []string{"b", "a"}},
		{"revisited tile", []string{"a", "b", "c", "d", "c"}, []string{"c", "d", "b"}, []string{"d", "b", "a"}},
		{"background close", []string{"a", "b", "c"}, []string{"b", "c"}, []string{"c", "a"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app := w.App()
				profileID := app.SelectedProfile()
				for _, id := range []string{"a", "b", "c", "d"} {
					injectAgent(t, w, id)
				}
				desktop, _ := viewProfile(t, w, profileID).paneOf(t, "a")
				for i, id := range row.visits {
					if i%2 == 0 {
						_, paneID := viewProfile(t, w, profileID).paneOf(t, id)
						requestID := uuid.NewString()
						mustProfileRequest(app, protocol.DesktopSetActivePaneMessage{Cmd: protocol.CmdDesktopSetActivePane, RequestID: requestID, DesktopID: desktop.ID, PaneID: paneID}, requestID)
						continue
					}
					if result := requestShowSession(app, id); !result.Success {
						t.Fatalf("show %s: %s", id, protocol.Deref(result.Error))
					}
				}
				for i, id := range row.closes {
					if result := closeFromApp(app, id); result.Error != nil {
						t.Fatal(*result.Error)
					}
					view := viewProfile(t, w, profileID)
					_, want := view.paneOf(t, row.wants[i])
					if got := view.desktops[desktop.ID].ActivePaneID; got != want {
						t.Fatalf("close %s selected %s, want %s (%s)", id, got, row.wants[i], want)
					}
				}
			})
		})
	}
}

func TestDesktopFocusHistorySurvivesRestartAndTileMoves(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		initial := viewProfile(t, w, profileID)
		source := initial.desktops[initial.profile.CurrentDesktopID]
		target := createDesktop(app, profileID)
		for _, row := range []struct{ desktop, tile string }{
			{source.ID, "a"}, {source.ID, "b"}, {source.ID, "c"}, {source.ID, "d"}, {target.ID, "a"}, {target.ID, "y"},
		} {
			result := dockOnDesktop(app, viewProfile(t, w, profileID).desktops[row.desktop], protocol.DesktopDockTileMessage{
				TileID: row.tile, TileKind: "browser", TileParams: protocol.Ptr("https://example.com"), Edge: protocol.LayoutDockEdgeRight,
			})
			if !result.Success {
				t.Fatal(protocol.Deref(result.Error))
			}
		}
		for _, leaf := range []string{"a", "b", "c", "d", "c"} {
			shownIn(t, requestShowLeaf(app, source.ID, leaf), source.ID, leaf)
		}
		w.restart()
		app = w.AppOn(profileID)
		move := func(leaf string) string {
			t.Helper()
			view := viewProfile(t, w, profileID)
			id := uuid.NewString()
			result := mustProfileRequest(app, protocol.DesktopMoveLeafMessage{
				Cmd: protocol.CmdDesktopMoveLeaf, RequestID: id, SourceDesktopID: source.ID, TargetDesktopID: target.ID, LeafID: leaf,
				Edge: protocol.LayoutDockEdgeRight, ExpectedSourceRevision: view.desktops[source.ID].Revision, ExpectedTargetRevision: view.desktops[target.ID].Revision,
			}, id)
			return protocol.Deref(result.PaneID)
		}
		move("c")
		if got := viewProfile(t, w, profileID).desktops[source.ID].ActivePaneID; got != "d" {
			t.Fatalf("moving focused c after restart selected %s, want d", got)
		}
		renamed := move("a")
		if renamed == "a" {
			t.Fatal("move should rename a because target already contains it")
		}
		if got := viewProfile(t, w, profileID).desktops[source.ID].ActivePaneID; got != "d" {
			t.Fatalf("moving background a selected %s, want d", got)
		}
		for _, row := range []struct{ desktop, close, want string }{
			{source.ID, "d", "b"}, {target.ID, renamed, "c"}, {target.ID, "c", "y"},
		} {
			current := viewProfile(t, w, profileID).desktops[row.desktop]
			id := uuid.NewString()
			result := mustProfileRequest(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, RequestID: id, DesktopID: row.desktop, LeafID: row.close, ExpectedRevision: current.Revision}, id)
			if got := result.Desktops[0].ActivePaneID; got != row.want {
				t.Fatalf("closing %s on %s selected %s, want %s", row.close, row.desktop, got, row.want)
			}
		}
	})
}

func TestAnAgentExitReturnsToThePreviouslyFocusedTile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	var sessions []string
	for _, name := range []string{"a", "b", "c"} {
		sessions = append(sessions, w.Spawn(app, fakeagent.Claude, w.Path(name)))
	}
	profileID := app.SelectedProfile()
	for _, id := range sessions {
		requestShowSession(app, id)
	}
	desktop, paneB := viewProfile(t, w, profileID).paneOf(t, sessions[1])
	watcher := w.AppOn(profileID)
	w.Launched(sessions[2]).Exit(0)
	testworld.Await(watcher, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == protocol.SessionID(sessions[2]) })
	if result := closeFromApp(app, sessions[2]); result.Error != nil {
		t.Fatal(*result.Error)
	}
	if got := viewProfile(t, w, profileID).desktops[desktop.ID].ActivePaneID; got != paneB {
		t.Fatalf("closing the exited agent selected %s, want previous tile %s", got, paneB)
	}
}

func TestUtilityTilesShareFocusHistoryWithAgentTiles(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		injectAgent(t, w, "b")
		desktop, paneA := viewProfile(t, w, profileID).paneOf(t, "a")
		_, paneB := viewProfile(t, w, profileID).paneOf(t, "b")
		mustDock := func(tileID string) {
			t.Helper()
			result := dockOnDesktop(app, viewProfile(t, w, profileID).desktops[desktop.ID], protocol.DesktopDockTileMessage{
				TileID: tileID, TileKind: "browser", TileParams: protocol.Ptr("https://example.com"), Edge: protocol.LayoutDockEdgeRight,
			})
			if !result.Success {
				t.Fatal(protocol.Deref(result.Error))
			}
		}
		mustDock("notes")
		mustDock("reference")
		for _, leaf := range []string{paneA, paneB, "notes", "reference", "notes"} {
			shownIn(t, requestShowLeaf(app, desktop.ID, leaf), desktop.ID, leaf)
		}
		for _, row := range []struct{ close, want string }{{"notes", "reference"}, {"reference", paneB}} {
			current := viewProfile(t, w, profileID).desktops[desktop.ID]
			id := uuid.NewString()
			removed := mustProfileRequest(app, protocol.DesktopRemoveLeafMessage{
				Cmd: protocol.CmdDesktopRemoveLeaf, RequestID: id, DesktopID: desktop.ID, LeafID: row.close, ExpectedRevision: current.Revision,
			}, id)
			if removed.Desktops[0].ActivePaneID != row.want {
				t.Fatalf("close %s selected %s, want %s", row.close, removed.Desktops[0].ActivePaneID, row.want)
			}
		}
	})
}
