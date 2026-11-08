package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func popOut(p *testworld.Peer, desktopID, leafID string, frame protocol.TileWindowFrame) protocol.ProfileActionResultMessage {
	p.T.Helper()
	id := uuid.NewString()
	return profileRequest(p, protocol.DesktopPopOutLeafMessage{Cmd: protocol.CmdDesktopPopOutLeaf, RequestID: id, DesktopID: desktopID, LeafID: leafID, Frame: frame}, id)
}

func bringBack(p *testworld.Peer, desktopID, leafID string) protocol.ProfileActionResultMessage {
	p.T.Helper()
	id := uuid.NewString()
	return profileRequest(p, protocol.DesktopBringBackLeafMessage{Cmd: protocol.CmdDesktopBringBackLeaf, RequestID: id, DesktopID: desktopID, LeafID: leafID}, id)
}

func (n layoutNode) leaf(id string) (layoutNode, bool) {
	if (n.Type == "pane" && n.PaneID == id) || (n.Type == "tile" && n.TileID == id) {
		return n, true
	}
	for _, child := range n.Children {
		if found, ok := child.leaf(id); ok {
			return found, true
		}
	}
	return layoutNode{}, false
}

func leafWindow(t *testing.T, desktop protocol.Desktop, leafID string) *protocol.TileWindowFrame {
	t.Helper()
	leaf, ok := desktopTree(t, desktop).leaf(leafID)
	if !ok {
		t.Fatalf("leaf %s is not in desktop %s: %s", leafID, desktop.ID, desktop.TreeJson)
	}
	return leaf.Window
}

func awaitLeafWindow(t *testing.T, p *testworld.Peer, desktopID, leafID string, want *protocol.TileWindowFrame) {
	t.Helper()
	testworld.Await(p, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
		desktop, ok := desktopIn(e.Desktops, desktopID)
		if !ok {
			return false
		}
		got := leafWindow(t, desktop, leafID)
		return (got == nil) == (want == nil) && (got == nil || *got == *want)
	})
}

func TestAPoppedTileKeepsItsPlaceAndItsFrameUntilItIsBroughtBack(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		placed, _ := viewProfile(t, w, profileID).paneOf(t, "a")
		paneB := "tile-notes"
		if docked := dockOnDesktop(app, placed, protocol.DesktopDockTileMessage{
			TileID: paneB, TileKind: "markdown", TileParams: protocol.Ptr(writeNotes(t, w)), Edge: protocol.LayoutDockEdgeRight,
		}); !docked.Success {
			t.Fatalf("docking a tile: %s", protocol.Deref(docked.Error))
		}
		desktop := viewProfile(t, w, profileID).desktops[placed.ID]
		leaves := desktopTree(t, desktop).leafIDs()
		watcher := w.AppOn(profileID)

		lifted := protocol.TileWindowFrame{X: 120, Y: 80, Width: 640, Height: 480}
		popped := popOut(app, desktop.ID, paneB, lifted)
		if !popped.Success || len(popped.Desktops) != 1 {
			t.Fatalf("popping out %s: success=%v error=%q", paneB, popped.Success, protocol.Deref(popped.Error))
		}
		if got := leafWindow(t, popped.Desktops[0], paneB); got == nil || *got != lifted {
			t.Fatalf("popped leaf %s has window %+v, want %+v", paneB, got, lifted)
		}
		if got := desktopTree(t, popped.Desktops[0]).leafIDs(); !slices.Equal(got, leaves) {
			t.Fatalf("popping out changed the tree's leaves from %v to %v", leaves, got)
		}
		awaitLeafWindow(t, watcher, desktop.ID, paneB, &lifted)

		moved := protocol.TileWindowFrame{X: -300.5, Y: 40, Width: 800, Height: 600.5}
		saved := popOut(app, desktop.ID, paneB, moved)
		if !saved.Success {
			t.Fatalf("saving a moved frame: %s", protocol.Deref(saved.Error))
		}
		if got := leafWindow(t, saved.Desktops[0], paneB); got == nil || *got != moved {
			t.Fatalf("after saving a moved frame leaf %s has window %+v, want %+v", paneB, got, moved)
		}
		awaitLeafWindow(t, watcher, desktop.ID, paneB, &moved)

		w.restart()
		restarted := viewProfile(t, w, profileID).desktops[desktop.ID]
		if got := leafWindow(t, restarted, paneB); got == nil || *got != moved {
			t.Fatalf("after a restart leaf %s has window %+v, want %+v", paneB, got, moved)
		}

		app = w.App()
		watcher = w.AppOn(profileID)
		back := bringBack(app, desktop.ID, paneB)
		if !back.Success || leafWindow(t, back.Desktops[0], paneB) != nil {
			t.Fatalf("bringing back %s: success=%v error=%q", paneB, back.Success, protocol.Deref(back.Error))
		}
		if got := desktopTree(t, back.Desktops[0]).leafIDs(); !slices.Equal(got, []string{paneB}) {
			t.Fatalf("bringing back left the restarted tree with leaves %v, want only %s", got, paneB)
		}
		awaitLeafWindow(t, watcher, desktop.ID, paneB, nil)

		again := bringBack(app, desktop.ID, paneB)
		if !again.Success || leafWindow(t, again.Desktops[0], paneB) != nil {
			t.Fatalf("bringing back a leaf that is not popped: success=%v error=%q", again.Success, protocol.Deref(again.Error))
		}
	})
}

func TestAPopOutWithoutRoomOrALeafIsRefusedAndChangesNothing(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		desktop, paneA := viewProfile(t, w, profileID).paneOf(t, "a")
		counter := countArrangements(app)

		for _, tc := range []struct {
			name   string
			result protocol.ProfileActionResultMessage
			code   protocol.ProfileErrorCode
			names  string
		}{
			{"a frame with no width", popOut(app, desktop.ID, paneA, protocol.TileWindowFrame{Width: 0, Height: 300}), protocol.ProfileErrorCodeInvalid, "0x300"},
			{"a frame with a negative height", popOut(app, desktop.ID, paneA, protocol.TileWindowFrame{Width: 400, Height: -1}), protocol.ProfileErrorCodeInvalid, "400x-1"},
			{"a leaf of no desktop", popOut(app, desktop.ID, "pane-missing", protocol.TileWindowFrame{Width: 400, Height: 300}), protocol.ProfileErrorCodeNotFound, "pane-missing"},
			{"bringing back a leaf of no desktop", bringBack(app, desktop.ID, "pane-missing"), protocol.ProfileErrorCodeNotFound, "pane-missing"},
		} {
			code, message := protocol.Deref(tc.result.ErrorCode), protocol.Deref(tc.result.Error)
			if tc.result.Success || code != tc.code || !strings.Contains(message, tc.names) {
				t.Errorf("%s: success=%v code=%q error=%q, want %s naming %q", tc.name, tc.result.Success, code, message, tc.code, tc.names)
			}
		}
		counter.want(t, "refused pop outs", 0)
		after := viewProfile(t, w, profileID).desktops[desktop.ID]
		if after.Revision != desktop.Revision || leafWindow(t, after, paneA) != nil {
			t.Fatalf("refused pop outs changed desktop %s: revision %d -> %d, tree %s", desktop.ID, desktop.Revision, after.Revision, after.TreeJson)
		}
	})
}

func TestShowingAPoppedTileLeavesMainOnItsDesktop(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		injectAgent(t, w, "neighbour")
		first, paneA := viewProfile(t, w, profileID).paneOf(t, "a")
		_, neighbour := viewProfile(t, w, profileID).paneOf(t, "neighbour")
		shownIn(t, requestShowLeaf(app, first.ID, neighbour), first.ID, neighbour)
		frame := protocol.TileWindowFrame{X: 10, Y: 10, Width: 500, Height: 400}
		if popped := popOut(app, first.ID, paneA, frame); !popped.Success {
			t.Fatalf("popping out %s: %s", paneA, protocol.Deref(popped.Error))
		}
		second := createDesktop(app, profileID)
		switchDesktop(app, profileID, second.ID)
		watcher := w.AppOn(profileID)

		for _, tc := range []struct {
			name string
			show func() protocol.ProfileActionResultMessage
		}{
			{"show_leaf", func() protocol.ProfileActionResultMessage { return requestShowLeaf(app, first.ID, paneA) }},
			{"show_session", func() protocol.ProfileActionResultMessage { return requestShowSession(app, "a") }},
		} {
			shownIn(t, requestShowLeaf(app, first.ID, neighbour), first.ID, neighbour)
			switchDesktop(app, profileID, second.ID)
			result := tc.show()
			if !result.Success || protocol.Deref(result.PaneID) != paneA || result.Profile == nil || len(result.Desktops) != 1 {
				t.Fatalf("%s of popped %s: success=%v error=%q pane %q", tc.name, paneA, result.Success, protocol.Deref(result.Error), protocol.Deref(result.PaneID))
			}
			if result.Profile.CurrentDesktopID != second.ID || result.Desktops[0].ActivePaneID != paneA {
				t.Fatalf("%s of popped %s left main on %s with %s active on %s, want main on %s and %s active",
					tc.name, paneA, result.Profile.CurrentDesktopID, result.Desktops[0].ActivePaneID, first.ID, second.ID, paneA)
			}
			if got := leafWindow(t, result.Desktops[0], paneA); got == nil || *got != frame {
				t.Fatalf("%s of popped %s left it with window %+v, want its saved %+v", tc.name, paneA, got, frame)
			}
			testworld.Await(watcher, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
				desktop, ok := desktopIn(e.Desktops, first.ID)
				return ok && e.Profile.CurrentDesktopID == second.ID && desktop.ActivePaneID == paneA
			})
		}
	})
}

func TestClosingATileInMainActivatesATileStillInMain(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		injectAgent(t, w, "b")
		injectAgent(t, w, "c")
		view := viewProfile(t, w, profileID)
		desktop, paneA := view.paneOf(t, "a")
		_, paneB := view.paneOf(t, "b")
		_, paneC := view.paneOf(t, "c")
		if got := desktopTree(t, desktop).leafIDs(); !slices.Equal(got, []string{paneA, paneB, paneC}) {
			t.Fatalf("three agents placed as %v, want %v in a row", got, []string{paneA, paneB, paneC})
		}
		if popped := popOut(app, desktop.ID, paneC, protocol.TileWindowFrame{Width: 500, Height: 400}); !popped.Success {
			t.Fatalf("popping out %s: %s", paneC, protocol.Deref(popped.Error))
		}
		shownIn(t, requestShowLeaf(app, desktop.ID, paneB), desktop.ID, paneB)

		if closed := closeFromApp(app, "b"); closed.Error != nil {
			t.Fatalf("closing b: %s", *closed.Error)
		}
		change := testworld.Await(app, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
			shown, ok := desktopIn(e.Desktops, desktop.ID)
			return ok && slices.Equal(desktopTree(t, shown).leafIDs(), []string{paneA, paneC})
		})
		shown, _ := desktopIn(change.Desktops, desktop.ID)
		if shown.ActivePaneID != paneA {
			t.Fatalf("closing %s between %s and popped %s made %q active, want %s", paneB, paneA, paneC, shown.ActivePaneID, paneA)
		}
	})
}
