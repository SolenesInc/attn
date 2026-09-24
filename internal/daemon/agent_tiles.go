package daemon

import (
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

type agentLocation struct {
	sessionID string
	profileID string
	desktopID string
	paneID    string
}

func (d *Daemon) currentAgent(callerSessionID string) (agentLocation, error) {
	if sessionID := strings.TrimSpace(callerSessionID); sessionID != "" {
		return d.agentLocation(sessionID)
	}
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		return agentLocation{}, err
	}
	location := agentLocation{profileID: profile.ID, desktopID: profile.CurrentDesktopID}
	if location.desktopID == "" {
		return location, nil
	}
	desktop, err := d.store.GetDesktop(location.desktopID)
	if err != nil {
		return agentLocation{}, err
	}
	location.paneID = desktop.ActivePaneID
	for _, pane := range desktop.Panes {
		if pane.PaneID == desktop.ActivePaneID && pane.Kind == profiles.PaneKindAgent {
			location.sessionID = pane.SessionID
		}
	}
	return location, nil
}

func (d *Daemon) agentLocation(sessionID string) (agentLocation, error) {
	profileID, err := d.store.SessionProfileID(sessionID)
	if err != nil {
		return agentLocation{}, err
	}
	if profileID == "" {
		return agentLocation{}, profiles.Errorf(profiles.CodeNotFound, "session %s belongs to no profile", sessionID)
	}
	location := agentLocation{sessionID: sessionID, profileID: profileID}
	placement, placed, err := d.store.SessionPlacement(sessionID)
	if err != nil {
		return agentLocation{}, err
	}
	if placed {
		location.desktopID, location.paneID = placement.DesktopID, placement.PaneID
		return location, nil
	}
	profile, err := d.store.LiveProfile(profileID)
	if err != nil {
		return agentLocation{}, err
	}
	location.desktopID = profile.CurrentDesktopID
	return location, nil
}

func (d *Daemon) refreshCurrentAgent() {
	location, err := d.currentAgent("")
	if err != nil {
		d.logf("current agent: %v", err)
	}
	d.currentAgentMu.Lock()
	previous := d.currentAgentSessionID
	d.currentAgentSessionID = location.sessionID
	d.currentAgentMu.Unlock()
	if previous != location.sessionID {
		d.updateNudgeSelection(previous, location.sessionID)
	}
}

func (d *Daemon) currentAgentSession() string {
	d.currentAgentMu.RLock()
	defer d.currentAgentMu.RUnlock()
	return d.currentAgentSessionID
}

type agentTile struct {
	tileID    string
	tileKind  string
	params    string
	sessionID string
}

func (d *Daemon) openAgentTile(location agentLocation, tile agentTile) (profiles.Desktop, string, error) {
	if location.desktopID == "" {
		return profiles.Desktop{}, "", profiles.Errorf(profiles.CodeNotFound, "profile %s has no current desktop to open a %s tile on", location.profileID, tile.tileKind)
	}
	for attempt := 1; ; attempt++ {
		if location.sessionID != "" {
			if placed, err := d.agentLocation(location.sessionID); err == nil {
				location = placed
			}
		}
		desktop, err := d.store.GetDesktop(location.desktopID)
		if err != nil {
			return desktop, "", err
		}
		tileID := openTileID(desktop, tile)
		edit, err := d.agentTileEdit(desktop, location.paneID, tile, tileID)
		if err != nil {
			return desktop, "", err
		}
		updated, err := d.store.UpdateDesktopArrangement(desktop.ID, desktop.Revision, edit)
		var profileErr *profiles.Error
		if errors.As(err, &profileErr) && profileErr.Code == profiles.CodeStaleRevision && attempt < 3 {
			continue
		}
		if err != nil {
			return updated, "", err
		}
		d.publishArrangementChanged(updated.ProfileID)
		return updated, tileID, nil
	}
}

func openTileID(desktop profiles.Desktop, tile agentTile) string {
	if tile.tileKind == string(layouttree.TileKindMarkdown) {
		for _, leaf := range layouttree.TileLeaves(desktop.Tree) {
			if leaf.TileKind == tile.tileKind && leaf.TileParams == tile.params {
				return leaf.TileID
			}
		}
	}
	return tile.tileID
}

func (d *Daemon) agentTileEdit(desktop profiles.Desktop, anchorPaneID string, tile agentTile, tileID string) (func(profiles.Desktop) (profiles.Desktop, error), error) {
	if _, docked := tileLeafByID(desktop.Tree, tileID); docked {
		update, err := d.checkedDesktopTileUpdate(desktop.ID, desktopTileUpdate{tileID: tileID, params: tile.params, hasParams: tile.params != "", sessionID: tile.sessionID})
		return func(desktop profiles.Desktop) (profiles.Desktop, error) {
			updated, err := applyDesktopTileUpdate(desktop, update)
			if err != nil {
				return updated, err
			}
			updated.Tree, _ = layouttree.UpdateTileSessionID(updated.Tree, tileID, tile.sessionID)
			updated.ActivePaneID = tileID
			return updated, nil
		}, err
	}
	dock, err := d.resolvedDesktopTileDock(desktop.ID, desktopTileDock{
		tileID:    tileID,
		tileKind:  tile.tileKind,
		params:    tile.params,
		sessionID: tile.sessionID,
		anchorID:  anchorPaneID,
		edge:      protocol.LayoutDockEdgeRight,
	})
	return func(desktop profiles.Desktop) (profiles.Desktop, error) {
		return dockTileOnDesktop(desktop, dock)
	}, err
}
