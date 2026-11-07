package daemon_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAStackRemembersItsMostRecentlyUsedTileAcrossARestart(t *testing.T) {
	for _, recent := range []string{"top", "bottom"} {
		t.Run(recent, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app := w.App()
				profileID := app.SelectedProfile()
				initial := viewProfile(t, w, profileID)
				desktopID := initial.profile.CurrentDesktopID
				for _, dock := range []protocol.DesktopDockTileMessage{
					{TileID: "single", Edge: protocol.LayoutDockEdgeRight},
					{TileID: "top", Edge: protocol.LayoutDockEdgeRight},
					{TileID: "bottom", AnchorID: protocol.Ptr("top"), Edge: protocol.LayoutDockEdgeBottom},
				} {
					dock.TileKind, dock.TileParams = "browser", protocol.Ptr("https://example.com")
					if result := dockOnDesktop(app, viewProfile(t, w, profileID).desktops[desktopID], dock); !result.Success {
						t.Fatal(protocol.Deref(result.Error))
					}
				}
				for _, leaf := range []string{recent, "single"} {
					shownIn(t, requestShowLeaf(app, desktopID, leaf), desktopID, leaf)
				}
				w.restart()
				app = w.AppOn(profileID)
				id := uuid.NewString()
				result := mustProfileRequest(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, RequestID: id, DesktopID: desktopID, LeafID: "single"}, id)
				if got := result.Desktops[0].ActivePaneID; got != recent {
					t.Fatalf("closing the single tile selected %s, want the stack's most recently used %s", got, recent)
				}
			})
		})
	}
}

func TestClosingAUtilityTileFocusesByPositionLikeAnAgentTile(t *testing.T) {
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

			id := uuid.NewString()
			removed := mustProfileRequest(app, protocol.DesktopRemoveLeafMessage{
				Cmd: protocol.CmdDesktopRemoveLeaf, RequestID: id, DesktopID: desktop.ID, LeafID: row.close,
			}, id)
			if removed.Desktops[0].ActivePaneID != row.want {
				t.Fatalf("close %s selected %s, want %s", row.close, removed.Desktops[0].ActivePaneID, row.want)
			}
		}
	})
}
