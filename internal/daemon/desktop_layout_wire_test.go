package daemon_test

import (
	"math"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func desktopRequest(app *testworld.Peer, cmd any, requestID string) protocol.ProfileActionResultMessage {
	app.T.Helper()
	return testworld.Request(app, cmd, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == requestID })
}

func changedDesktop(t *testing.T, result protocol.ProfileActionResultMessage, desktopID string) protocol.Desktop {
	t.Helper()
	return desktopNamed(t, result.Desktops, desktopID)
}

func desktopNamed(t *testing.T, desktops []protocol.Desktop, desktopID string) protocol.Desktop {
	t.Helper()
	for _, desktop := range desktops {
		if desktop.ID == desktopID {
			return desktop
		}
	}
	t.Fatalf("no desktop %s among %d", desktopID, len(desktops))
	return protocol.Desktop{}
}

func spawnSideBySide(t *testing.T, w *world, app *testworld.Peer, cwd string) (protocol.Desktop, string, string) {
	t.Helper()
	first, desktopID, firstPane := w.RequestSpawn(app, shellHarness, cwd)
	second, _, secondPane := w.RequestSpawn(app, shellHarness, cwd)
	if !first.Success || !second.Success {
		t.Fatalf("spawning two shells failed: %s / %s", protocol.Deref(first.Error), protocol.Deref(second.Error))
	}
	desktop := awaitDesktop(app, desktopID, func(d protocol.Desktop) bool {
		leaves := desktopTree(t, d).leafIDs()
		return slices.Contains(leaves, firstPane) && slices.Contains(leaves, secondPane)
	})
	return desktop, firstPane, secondPane
}

func TestASplitRatioTheUserSetStaysLockedAsPanesComeAndGoAndAcrossARestart(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	desktop, _, secondPane := spawnSideBySide(t, w, app, cwd)
	split, _, ok := desktopTree(t, desktop).splitHolding(secondPane)
	if !ok {
		t.Fatal("two panes share no split")
	}

	for _, c := range []struct {
		split   string
		success bool
	}{
		{"split-that-does-not-exist", false},
		{split.SplitID, true},
	} {
		requestID := uuid.NewString()
		result := desktopRequest(app, protocol.DesktopSetSplitRatioMessage{
			Cmd: protocol.CmdDesktopSetSplitRatio, DesktopID: desktop.ID, SplitID: c.split, Ratio: 0.3,
			ExpectedRevision: desktop.Revision, RequestID: requestID,
		}, requestID)
		if result.Success != c.success {
			t.Fatalf("setting the ratio of %s answered success=%v (%s), want %v", c.split, result.Success, protocol.Deref(result.Error), c.success)
		}
		if result.Success {
			desktop = changedDesktop(t, result, desktop.ID)
		}
	}

	third := w.Spawn(app, shellHarness, cwd)
	if closed := closeFromApp(app, third); !closed.Accepted {
		t.Fatalf("closing the third shell failed: %s", protocol.Deref(closed.Error))
	}
	for _, when := range []string{"after a pane came and went", "after a restart"} {
		if when == "after a restart" {
			w.restart()
		}
		latest := desktopNamed(t, w.App().Initial.Desktops, desktop.ID)
		if slices.Contains(desktopTree(t, latest).leafIDs(), "pane-"+third) {
			t.Fatalf("%s the closed shell still has a pane: %s", when, latest.TreeJson)
		}
		locked, found := desktopTree(t, latest).split(split.SplitID)
		if !found || !locked.RatioLocked || math.Abs(locked.Ratio-0.3) > 0.01 {
			t.Errorf("%s the split is %+v, want it locked at 0.3", when, locked)
		}
	}
}

func TestMovingAPaneWithinItsDesktopResplitsAroundTheDropTarget(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	desktop, firstPane, secondPane := spawnSideBySide(t, w, app, w.Path("shop"))
	move := func(anchor string, edge protocol.LayoutDockEdge) protocol.ProfileActionResultMessage {
		requestID := uuid.NewString()
		return desktopRequest(app, protocol.DesktopMoveLeafMessage{
			Cmd: protocol.CmdDesktopMoveLeaf, SourceDesktopID: desktop.ID, TargetDesktopID: desktop.ID,
			LeafID: firstPane, AnchorID: protocol.Ptr(anchor), Edge: edge, LeafShare: protocol.Ptr(0.5),
			ExpectedSourceRevision: desktop.Revision, ExpectedTargetRevision: desktop.Revision, RequestID: requestID,
		}, requestID)
	}

	if selfDrop := move(firstPane, protocol.LayoutDockEdgeRight); selfDrop.Success {
		t.Fatal("dropping a pane on itself was accepted")
	}

	moved := move(secondPane, protocol.LayoutDockEdgeBottom)
	if !moved.Success {
		t.Fatalf("moving the pane below its neighbour failed: %s", protocol.Deref(moved.Error))
	}
	after := changedDesktop(t, moved, desktop.ID)
	root := desktopTree(t, after)
	if root.Type != "split" || root.Direction != "horizontal" || !slices.Equal(root.leafIDs(), []string{secondPane, firstPane}) {
		t.Errorf("after dropping %s below %s the tree is %s, want a top/bottom split of the two", firstPane, secondPane, after.TreeJson)
	}
	if len(after.Panes) != 2 {
		t.Errorf("after the move the panes are %+v, want both agents kept", after.Panes)
	}
}
