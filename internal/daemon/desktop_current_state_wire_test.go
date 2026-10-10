package daemon_test

import (
	"math"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func desktopOperation(app *testworld.Peer, cmd string, fields map[string]any) protocol.ProfileActionResultMessage {
	app.T.Helper()
	fields["cmd"], fields["request_id"] = cmd, uuid.NewString()
	return mustProfileRequest(app, fields, fields["request_id"].(string))
}

func dockCurrentStateTile(app *testworld.Peer, desktopID, tileID string) protocol.Desktop {
	app.T.Helper()
	result := desktopOperation(app, protocol.CmdDesktopDockTile, map[string]any{
		"desktop_id": desktopID, "tile_id": tileID, "tile_kind": "notebook", "edge": "right",
	})
	return result.Desktops[0]
}

func TestDesktopOperationsApplyToCurrentStateAcrossClients(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	other := w.AppOn(app.SelectedProfile())
	first := app.Initial.Desktops[0]
	second := createDesktop(app, app.SelectedProfile())
	third := createDesktop(app, app.SelectedProfile())

	desktopOperation(other, protocol.CmdDesktopRename, map[string]any{"desktop_id": first.ID, "name": "Elsewhere"})
	dockCurrentStateTile(app, first.ID, "notes")
	dockCurrentStateTile(other, first.ID, "reference")
	desktopOperation(app, protocol.CmdDesktopUpdateTile, map[string]any{"desktop_id": first.ID, "tile_id": "notes", "tile_params": "/tmp/notes"})
	desktopOperation(other, protocol.CmdDesktopRename, map[string]any{"desktop_id": first.ID, "name": "Changed"})
	current := viewProfile(t, w, app.SelectedProfile()).desktops[first.ID]
	split, _, found := desktopTree(t, current).splitHolding("notes")
	if !found {
		t.Fatal("two tiles share no split")
	}
	ratioed := desktopOperation(app, protocol.CmdDesktopSetSplitRatio, map[string]any{"desktop_id": first.ID, "split_id": split.SplitID, "ratio": 0.3})
	if got, ok := desktopTree(t, ratioed.Desktops[0]).split(split.SplitID); !ok || math.Abs(got.Ratio-0.3) > 1e-9 {
		t.Fatalf("resize on the current tree: %+v, found=%v", got, ok)
	}
	renamed := desktopOperation(app, protocol.CmdDesktopRename, map[string]any{"desktop_id": first.ID, "name": "Final"}).Desktops[0]
	if renamed.Name != "Final" || !slices.Equal(desktopTree(t, renamed).leafIDs(), []string{"notes", "reference"}) {
		t.Fatalf("rename discarded another action: %+v", renamed)
	}
	desktopOperation(app, protocol.CmdDesktopReorder, map[string]any{"desktop_id": first.ID, "previous_desktop_id": second.ID, "next_desktop_id": third.ID})
	removed := desktopOperation(app, protocol.CmdDesktopRemoveLeaf, map[string]any{"desktop_id": first.ID, "leaf_id": "reference"}).Desktops[0]
	if !slices.Equal(desktopTree(t, removed).leafIDs(), []string{"notes"}) || removed.Name != "Final" {
		t.Fatalf("removing reference discarded current state: %+v", removed)
	}
}

func TestDesktopMoveChoosesCurrentPlacementAndKeepsExplicitDragGeometry(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	other := w.AppOn(app.SelectedProfile())
	source := app.Initial.Desktops[0]
	target := createDesktop(app, app.SelectedProfile())
	dockCurrentStateTile(app, source.ID, "moving")
	dockCurrentStateTile(app, source.ID, "dragged")
	dockCurrentStateTile(app, target.ID, "anchor")
	dockCurrentStateTile(other, target.ID, "background")
	shownIn(t, requestShowLeaf(other, target.ID, "anchor"), target.ID, "anchor")
	desktopOperation(other, protocol.CmdDesktopRename, map[string]any{"desktop_id": source.ID, "name": "Changed source"})
	moved := desktopOperation(app, protocol.CmdDesktopMoveLeaf, map[string]any{
		"source_desktop_id": source.ID, "target_desktop_id": target.ID, "leaf_id": "moving",
	})
	current := changedDesktop(t, moved, target.ID)
	split, _, found := desktopTree(t, current).splitHolding("moving")
	if !found || !slices.Equal(split.leafIDs(), []string{"anchor", "moving"}) || split.Direction != "vertical" {
		t.Fatalf("keyboard move did not use current active anchor: %s", current.TreeJson)
	}
	desktopOperation(other, protocol.CmdDesktopRename, map[string]any{"desktop_id": target.ID, "name": "Changed target"})
	dragged := desktopOperation(app, protocol.CmdDesktopMoveLeaf, map[string]any{
		"source_desktop_id": source.ID, "target_desktop_id": target.ID, "leaf_id": "dragged", "anchor_id": "anchor", "edge": "bottom", "leaf_share": 0.25,
	})
	current = changedDesktop(t, dragged, target.ID)
	split, _, found = desktopTree(t, current).splitHolding("dragged")
	if !found || !slices.Equal(split.leafIDs(), []string{"anchor", "dragged"}) || split.Direction != "horizontal" || math.Abs(split.Ratio-0.75) > 1e-9 {
		t.Fatalf("drag lost requested anchor, edge or share: %s", current.TreeJson)
	}
}

func TestDesktopMoveResolvesOrCreatesDestinationAtomically(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	profileID := app.SelectedProfile()
	source := app.Initial.Desktops[0]
	dockCurrentStateTile(app, source.ID, "slot-move")
	dockCurrentStateTile(app, source.ID, "new-move")
	dockCurrentStateTile(app, source.ID, "existing-move")
	shownIn(t, requestShowLeaf(app, source.ID, "slot-move"), source.ID, "slot-move")
	desktopOperation(app, protocol.CmdDesktopMoveLeaf, map[string]any{
		"source_desktop_id": source.ID, "leaf_id": "slot-move", "target_shortcut_slot": 5,
	})
	desktopOperation(app, protocol.CmdDesktopMoveLeaf, map[string]any{
		"source_desktop_id": source.ID, "leaf_id": "new-move",
	})
	desktopOperation(app, protocol.CmdDesktopMoveLeaf, map[string]any{
		"source_desktop_id": source.ID, "leaf_id": "existing-move", "target_shortcut_slot": 5,
	})
	current := w.AppOn(profileID).Initial
	if len(current.Desktops) != 3 || current.Profiles[0].CurrentDesktopID != source.ID {
		t.Fatalf("moves created extra desktops or followed without a gesture: %+v", current)
	}
	for _, desktop := range current.Desktops {
		if desktop.ID == source.ID {
			if desktop.TreeJson != "" {
				t.Fatalf("source kept tiles after the moves: %s", desktop.TreeJson)
			}
			continue
		}
		want := []string{"new-move"}
		if protocol.Deref(desktop.ShortcutSlot) == 5 {
			want = []string{"slot-move", "existing-move"}
		}
		if got := desktopTree(t, desktop).leafIDs(); !slices.Equal(got, want) {
			t.Fatalf("desktop %s at shortcut %d has tiles %v, want %v", desktop.ID, protocol.Deref(desktop.ShortcutSlot), got, want)
		}
	}
	requestID := uuid.NewString()
	refused := profileRequest(app, map[string]any{
		"cmd": protocol.CmdDesktopMoveLeaf, "request_id": requestID, "source_desktop_id": source.ID, "leaf_id": "missing", "target_shortcut_slot": 6,
	}, requestID)
	if refused.Success || protocol.Deref(refused.ErrorCode) != protocol.ProfileErrorCodeNotFound {
		t.Fatalf("missing leaf answered %+v", refused)
	}
	if after := len(w.AppOn(profileID).Initial.Desktops); after != len(current.Desktops) {
		t.Fatalf("refused move left an empty destination: desktops %d -> %d", len(current.Desktops), after)
	}
}
