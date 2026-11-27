package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
)

func TestATileShowsOneOfAttnsViewsByNameWhileViewsAreOn(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	placeSessions(t, w, app, cli, "s1")
	desktop := currentDesktop(t, w)
	dockView := func(tileID, params string) protocol.ProfileActionResultMessage {
		return dockOnDesktop(app, desktop, protocol.DesktopDockTileMessage{
			TileID: tileID, TileKind: "markdown", TileParams: protocol.Ptr(params), Edge: protocol.LayoutDockEdgeRight,
		})
	}

	if off := dockView("tile-sessions", `{"view":"sessions"}`); off.Success || !strings.Contains(protocol.Deref(off.Error), "views_enabled") {
		t.Fatalf("with views off, docking the Sessions view answered success=%v error %q, want a refusal naming views_enabled", off.Success, protocol.Deref(off.Error))
	}

	setSetting(t, app, "views_enabled", "true")
	var last protocol.ProfileActionResultMessage
	for _, view := range []string{"attn", "sessions", "pull-requests"} {
		last = dockView("tile-"+view, `{ "view": "`+view+`" }`)
		if !last.Success {
			t.Fatalf("docking the %s view failed: %s", view, protocol.Deref(last.Error))
		}
	}
	shown := map[string]string{}
	for _, d := range last.Desktops {
		if d.ID != desktop.ID {
			continue
		}
		tree, err := layouttree.DecodeLayout(d.TreeJson)
		if err != nil {
			t.Fatal(err)
		}
		for _, leaf := range layouttree.TileLeaves(tree) {
			shown[leaf.TileID] = leaf.TileKind + " " + leaf.TileParams
		}
	}
	for _, view := range []string{"attn", "sessions", "pull-requests"} {
		if got, want := shown["tile-"+view], `markdown {"view":"`+view+`"}`; got != want {
			t.Errorf("the %s tile reads %q on its desktop, want %q", view, got, want)
		}
	}

	unknown := dockView("tile-todo", `{"view":"todo"}`)
	if unknown.Success || !strings.Contains(protocol.Deref(unknown.Error), "attn, sessions and pull-requests") {
		t.Errorf("docking an unknown view answered success=%v error %q, want a refusal naming attn's views", unknown.Success, protocol.Deref(unknown.Error))
	}
	for _, e := range app.Received() {
		if e.Event == protocol.EventDesktopTileContent && slices.Contains([]string{"tile-attn", "tile-sessions", "tile-pull-requests"}, protocol.Deref(e.TileID)) {
			t.Errorf("the daemon sent file content to the view tile %s", protocol.Deref(e.TileID))
		}
	}
}
