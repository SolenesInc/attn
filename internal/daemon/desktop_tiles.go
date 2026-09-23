package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func tileLeafByID(tree layouttree.Node, tileID string) (layouttree.TileLeaf, bool) {
	for _, leaf := range layouttree.TileLeaves(tree) {
		if leaf.TileID == tileID {
			return leaf, true
		}
	}
	return layouttree.TileLeaf{}, false
}

func dockAnchor(desktop profiles.Desktop, requested, tileID string) string {
	for _, candidate := range []string{strings.TrimSpace(requested), desktop.ActivePaneID} {
		if candidate != "" && candidate != tileID && (layouttree.HasPane(desktop.Tree, candidate) || layouttree.HasTile(desktop.Tree, candidate)) {
			return candidate
		}
	}
	for _, leafID := range append(layouttree.PaneIDs(desktop.Tree), layouttree.TileIDs(desktop.Tree)...) {
		if leafID != tileID {
			return leafID
		}
	}
	return ""
}

type desktopTileDock struct {
	tileID    string
	tileKind  string
	params    string
	sessionID string
	anchorID  string
	edge      protocol.LayoutDockEdge
	share     float64
}

func dockTileOnDesktop(desktop profiles.Desktop, dock desktopTileDock) (profiles.Desktop, error) {
	if layouttree.HasPane(desktop.Tree, dock.tileID) {
		return desktop, profiles.Errorf(profiles.CodeInvalid, "%s is a pane of desktop %s, not a tile", dock.tileID, desktop.ID)
	}
	anchor := dockAnchor(desktop, dock.anchorID, dock.tileID)
	if anchor == "" {
		desktop.Tree = layouttree.Node{Type: "tile", TileID: dock.tileID, TileKind: dock.tileKind, TileParams: dock.params, TileSessionID: dock.sessionID}
		return desktop, nil
	}
	share := defaultTileFraction
	if fraction, ok := layouttree.TileFractionByID(desktop.Tree, dock.tileID); ok && fraction > 0 && fraction < 1 {
		share = fraction
	}
	if dock.share > 0 && dock.share < 1 {
		share = dock.share
	}
	direction, before := layoutDockEdge(dock.edge)
	firstChildShare := share
	if !before {
		firstChildShare = 1 - share
	}
	next, ok := layouttree.DockTile(desktop.Tree, anchor, direction, before, newWorkspaceLayoutEntityID("split"), dock.tileID, dock.tileKind, dock.params, dock.sessionID, firstChildShare)
	if !ok {
		return desktop, profiles.Errorf(profiles.CodeInvalid, "tile %s could not dock beside %s on desktop %s", dock.tileID, anchor, desktop.ID)
	}
	desktop.Tree = next
	return desktop, nil
}

func (d *Daemon) handleDesktopDockTile(client *wsClient, msg *protocol.DesktopDockTileMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		dock, err := d.resolvedDesktopTileDock(msg.DesktopID, desktopTileDock{
			tileID:    strings.TrimSpace(msg.TileID),
			tileKind:  strings.TrimSpace(msg.TileKind),
			params:    strings.TrimSpace(protocol.Deref(msg.TileParams)),
			sessionID: strings.TrimSpace(protocol.Deref(msg.TileSessionID)),
			anchorID:  protocol.Deref(msg.AnchorID),
			edge:      msg.Edge,
			share:     protocol.Deref(msg.TileShare),
		})
		if err != nil {
			return profileActionOutcome{}, err
		}
		desktop, err := d.store.UpdateDesktopArrangement(msg.DesktopID, int64(msg.ExpectedRevision), func(desktop profiles.Desktop) (profiles.Desktop, error) {
			return dockTileOnDesktop(desktop, dock)
		})
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) resolvedDesktopTileDock(desktopID string, dock desktopTileDock) (desktopTileDock, error) {
	if dock.tileID == "" {
		return dock, profiles.Errorf(profiles.CodeInvalid, "docking a tile needs tile_id")
	}
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil {
		return dock, err
	}
	if err := d.checkedTileSession(desktop, dock.sessionID); err != nil {
		return dock, err
	}
	existing, docked := tileLeafByID(desktop.Tree, dock.tileID)
	if !docked {
		dock.params, err = d.validatedNewTileParams(dock.tileKind, dock.params)
		return dock, err
	}
	if existing.TileKind != dock.tileKind {
		return dock, profiles.Errorf(profiles.CodeInvalid, "tile %s is a %s tile and cannot be docked as %s; dock a new tile instead", dock.tileID, existing.TileKind, dock.tileKind)
	}
	if dock.sessionID == "" {
		dock.sessionID = existing.TileSessionID
	}
	if dock.params == "" {
		dock.params = existing.TileParams
		return dock, nil
	}
	dock.params, err = d.validatedNewTileParams(dock.tileKind, dock.params)
	return dock, err
}

func (d *Daemon) checkedTileSession(desktop profiles.Desktop, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	if d.store.Get(sessionID) == nil {
		return profiles.Errorf(profiles.CodeNotFound, "session %s does not exist", sessionID)
	}
	profileID, err := d.store.SessionProfileID(sessionID)
	if err != nil {
		return err
	}
	if profileID != desktop.ProfileID {
		return profiles.Errorf(profiles.CodeCrossProfile, "session %s belongs to profile %q and desktop %s to profile %q; a tile can only follow an agent of its own profile", sessionID, profileID, desktop.ID, desktop.ProfileID)
	}
	return nil
}

func (d *Daemon) validatedNewTileParams(kind, params string) (string, error) {
	switch layouttree.TileKind(kind) {
	case layouttree.TileKindMarkdown:
		if params == "" {
			return "", profiles.Errorf(profiles.CodeInvalid, "a markdown tile needs the path of its file in tile_params")
		}
		return params, nil
	case layouttree.TileKindBrowser, layouttree.TileKindSeed, layouttree.TileKindNotebook:
		return d.validatedTileParams(kind, params)
	default:
		return "", profiles.Errorf(profiles.CodeInvalid, "tile kind %q is not one of markdown, browser, seed or notebook", kind)
	}
}

func (d *Daemon) validatedTileParams(kind, params string) (string, error) {
	switch kind {
	case string(layouttree.TileKindBrowser):
		url, err := validateBrowserURL(params)
		if err != nil {
			return "", profiles.Errorf(profiles.CodeInvalid, "%v", err)
		}
		return url, nil
	case string(layouttree.TileKindNotebook):
		return params, nil
	case string(layouttree.TileKindSeed):
		if err := d.requireHome(garden.Surface); err != nil {
			return "", err
		}
		if _, _, err := d.readSeed(params); err != nil {
			return "", profiles.Errorf(profiles.CodeNotFound, "%v", err)
		}
		return params, nil
	default:
		return "", profiles.Errorf(profiles.CodeInvalid, "tile parameters cannot be updated for tile kind %q", kind)
	}
}

type desktopTileUpdate struct {
	tileID    string
	params    string
	sessionID string
}

func (d *Daemon) checkedDesktopTileUpdate(desktopID string, update desktopTileUpdate) (desktopTileUpdate, error) {
	if update.tileID == "" || (update.params == "" && update.sessionID == "") {
		return update, profiles.Errorf(profiles.CodeInvalid, "updating a tile needs tile_id and tile_params or tile_session_id")
	}
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil {
		return update, err
	}
	tile, found := tileLeafByID(desktop.Tree, update.tileID)
	if !found {
		return update, profiles.Errorf(profiles.CodeNotFound, "tile %s does not belong to desktop %s", update.tileID, desktopID)
	}
	if err := d.checkedTileSession(desktop, update.sessionID); err != nil {
		return update, err
	}
	if tile.TileKind == string(layouttree.TileKindMarkdown) {
		if update.sessionID == "" {
			return update, profiles.Errorf(profiles.CodeInvalid, "a markdown tile keeps its file; only its session can change")
		}
		update.params = ""
		return update, nil
	}
	if update.params != "" {
		if update.params, err = d.validatedTileParams(tile.TileKind, update.params); err != nil {
			return update, err
		}
	}
	return update, nil
}

func applyDesktopTileUpdate(desktop profiles.Desktop, update desktopTileUpdate) (profiles.Desktop, error) {
	next, found := desktop.Tree, true
	if update.params != "" {
		next, found = layouttree.UpdateTileParams(next, update.tileID, update.params)
	}
	if found && update.sessionID != "" {
		next, found = layouttree.UpdateTileSessionID(next, update.tileID, update.sessionID)
	}
	if !found {
		return desktop, profiles.Errorf(profiles.CodeNotFound, "tile %s does not belong to desktop %s", update.tileID, desktop.ID)
	}
	desktop.Tree = next
	return desktop, nil
}

func (d *Daemon) handleDesktopUpdateTile(client *wsClient, msg *protocol.DesktopUpdateTileMessage) {
	d.runProfileAction(client, msg.Cmd, msg.RequestID, func() (profileActionOutcome, error) {
		update, err := d.checkedDesktopTileUpdate(msg.DesktopID, desktopTileUpdate{
			tileID:    strings.TrimSpace(msg.TileID),
			params:    strings.TrimSpace(protocol.Deref(msg.TileParams)),
			sessionID: strings.TrimSpace(protocol.Deref(msg.TileSessionID)),
		})
		if err != nil {
			return profileActionOutcome{}, err
		}
		desktop, err := d.store.UpdateDesktopArrangement(msg.DesktopID, int64(msg.ExpectedRevision), func(desktop profiles.Desktop) (profiles.Desktop, error) {
			return applyDesktopTileUpdate(desktop, update)
		})
		return d.desktopChanged(desktop), err
	})
}
