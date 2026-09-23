package daemon

import (
	"errors"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

const desktopTileContainerPrefix = "desktop:"

func desktopTileContainer(desktopID string) string {
	return desktopTileContainerPrefix + desktopID
}

func desktopIDFromTileContainer(container string) (string, bool) {
	return strings.CutPrefix(container, desktopTileContainerPrefix)
}

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
	if dock.tileID == "" || dock.tileKind == "" {
		return desktop, profiles.Errorf(profiles.CodeInvalid, "docking a tile needs tile_id and tile_kind")
	}
	if layouttree.HasPane(desktop.Tree, dock.tileID) {
		return desktop, profiles.Errorf(profiles.CodeInvalid, "%s is a pane of desktop %s, not a tile", dock.tileID, desktop.ID)
	}
	existing, docked := tileLeafByID(desktop.Tree, dock.tileID)
	if dock.params == "" && docked {
		dock.params = existing.TileParams
	}
	if dock.sessionID == "" && docked {
		dock.sessionID = existing.TileSessionID
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
		dock, err := d.checkedDesktopTileDock(desktopTileDock{
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

func (d *Daemon) checkedDesktopTileDock(dock desktopTileDock) (desktopTileDock, error) {
	if !knownTileKind(dock.tileKind) {
		return dock, profiles.Errorf(profiles.CodeInvalid, "tile kind %q is not one of markdown, browser, seed or notebook", dock.tileKind)
	}
	if dock.sessionID != "" && d.store.Get(dock.sessionID) == nil {
		return dock, profiles.Errorf(profiles.CodeNotFound, "session %s does not exist", dock.sessionID)
	}
	if dock.params == "" || dock.tileKind == string(layouttree.TileKindMarkdown) {
		return dock, nil
	}
	var err error
	dock.params, err = d.validatedTileParams(dock.tileKind, dock.params)
	return dock, err
}

func knownTileKind(kind string) bool {
	switch layouttree.TileKind(kind) {
	case layouttree.TileKindMarkdown, layouttree.TileKindBrowser, layouttree.TileKindSeed, layouttree.TileKindNotebook:
		return true
	default:
		return false
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
	if update.sessionID != "" && d.store.Get(update.sessionID) == nil {
		return update, profiles.Errorf(profiles.CodeNotFound, "session %s does not exist", update.sessionID)
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

func (d *Daemon) desktopMarkdownTilePath(desktopID, tileID string) (string, error) {
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil {
		return "", err
	}
	return markdownTilePathOn(desktop, tileID)
}

func markdownTilePathOn(desktop profiles.Desktop, tileID string) (string, error) {
	desktopID := desktop.ID
	tile, found := tileLeafByID(desktop.Tree, tileID)
	if !found {
		return "", profiles.Errorf(profiles.CodeNotFound, "tile %s does not belong to desktop %s", tileID, desktopID)
	}
	if tile.TileKind != string(layouttree.TileKindMarkdown) {
		return "", profiles.Errorf(profiles.CodeInvalid, "tile %s is a %s tile; only markdown tiles have content", tileID, tile.TileKind)
	}
	return strings.TrimSpace(tile.TileParams), nil
}

func desktopTileContentMessage(desktopID, tileID, path, content string, readErr error) protocol.DesktopTileContentMessage {
	message := protocol.DesktopTileContentMessage{
		Event:     protocol.EventDesktopTileContent,
		DesktopID: desktopID,
		TileID:    tileID,
		TileKind:  string(layouttree.TileKindMarkdown),
		Path:      path,
		Content:   content,
	}
	if readErr != nil {
		message.Error = protocol.Ptr(readErr.Error())
	}
	return message
}

func (d *Daemon) handleDesktopTileContentGet(client *wsClient, msg *protocol.DesktopTileContentGetMessage) {
	if err := d.requireHome("profiles and desktops"); err != nil {
		d.sendCommandError(client, msg.Cmd, err.Error())
		return
	}
	desktop, err := d.store.GetDesktop(msg.DesktopID)
	if err == nil && desktop.ProfileID != client.selectedProfile() {
		err = profiles.Errorf(profiles.CodeCrossProfile, "desktop %s belongs to profile %s, not the profile this client is on", desktop.ID, desktop.ProfileID)
	}
	path := ""
	if err == nil {
		path, err = markdownTilePathOn(desktop, msg.TileID)
	}
	if err != nil {
		d.sendCommandError(client, msg.Cmd, err.Error())
		return
	}
	if !client.subscribeTileContent(desktopTileContainer(msg.DesktopID), msg.TileID) {
		d.sendCommandError(client, msg.Cmd, "too many tile content subscriptions")
		return
	}
	content, readErr := readMarkdownFile(path)
	d.sendToClient(client, desktopTileContentMessage(msg.DesktopID, msg.TileID, path, content, readErr))
}

func (d *Daemon) broadcastDesktopTileContent(ref markdownTileRef, content string, readErr error) {
	if path, err := d.desktopMarkdownTilePath(ref.desktopID, ref.tileID); err != nil || path != ref.path {
		return
	}
	container := desktopTileContainer(ref.desktopID)
	d.wsHub.SendValueToMatchingClients(desktopTileContentMessage(ref.desktopID, ref.tileID, ref.path, content, readErr), func(client *wsClient) bool {
		return client.wantsTileContent(container, ref.tileID)
	})
}

type desktopSubscriber struct {
	client  *wsClient
	profile string
	keys    []string
}

func (d *Daemon) desktopSubscribers() []desktopSubscriber {
	var subscribers []desktopSubscriber
	if d.wsHub == nil {
		return subscribers
	}
	d.wsHub.ForEachClient(func(client *wsClient) {
		if keys := client.tileContentSubscriptionKeys(); len(keys) > 0 {
			subscribers = append(subscribers, desktopSubscriber{client: client, profile: client.selectedProfile(), keys: keys})
		}
	})
	return subscribers
}

type desktopReads struct {
	d    *Daemon
	seen map[string]*profiles.Desktop
}

func (r *desktopReads) get(desktopID string) (*profiles.Desktop, bool) {
	if desktop, read := r.seen[desktopID]; read {
		return desktop, true
	}
	desktop, err := r.d.store.GetDesktop(desktopID)
	var profileErr *profiles.Error
	switch {
	case err == nil:
		r.seen[desktopID] = &desktop
	case errors.As(err, &profileErr) && profileErr.Code == profiles.CodeNotFound:
		r.seen[desktopID] = nil
	default:
		return nil, false
	}
	return r.seen[desktopID], true
}

func subscribedMarkdownPath(desktop *profiles.Desktop, profileID, tileID string) (string, bool) {
	if desktop == nil || desktop.ProfileID != profileID {
		return "", false
	}
	tile, found := tileLeafByID(desktop.Tree, tileID)
	path := strings.TrimSpace(tile.TileParams)
	return path, found && tile.TileKind == string(layouttree.TileKindMarkdown) && path != ""
}

func (d *Daemon) addSubscribedDesktopMarkdownTiles(desired map[string]markdownTileRef) {
	reads := &desktopReads{d: d, seen: make(map[string]*profiles.Desktop)}
	for _, subscriber := range d.desktopSubscribers() {
		for _, key := range subscriber.keys {
			container, tileID, _ := strings.Cut(key, "\x00")
			desktopID, isDesktop := desktopIDFromTileContainer(container)
			if !isDesktop {
				continue
			}
			desktop, known := reads.get(desktopID)
			if !known {
				continue
			}
			path, live := subscribedMarkdownPath(desktop, subscriber.profile, tileID)
			if !live {
				subscriber.client.dropTileContentSubscription(container, tileID)
				continue
			}
			desired[key] = markdownTileRef{desktopID: desktopID, tileID: tileID, path: path}
		}
	}
}
