package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setups"
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

func dockAnchor(desktop setups.Desktop, requested, tileID string) string {
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

func dockTileOnDesktop(desktop setups.Desktop, dock desktopTileDock) (setups.Desktop, error) {
	if dock.tileID == "" || dock.tileKind == "" {
		return desktop, setups.Errorf(setups.CodeInvalid, "docking a tile needs tile_id and tile_kind")
	}
	if layouttree.HasPane(desktop.Tree, dock.tileID) {
		return desktop, setups.Errorf(setups.CodeInvalid, "%s is a pane of desktop %s, not a tile", dock.tileID, desktop.ID)
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
		return desktop, setups.Errorf(setups.CodeInvalid, "tile %s could not dock beside %s on desktop %s", dock.tileID, anchor, desktop.ID)
	}
	desktop.Tree = next
	return desktop, nil
}

func (d *Daemon) handleDesktopDockTile(client *wsClient, msg *protocol.DesktopDockTileMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		dock := desktopTileDock{
			tileID:    strings.TrimSpace(msg.TileID),
			tileKind:  strings.TrimSpace(msg.TileKind),
			params:    strings.TrimSpace(protocol.Deref(msg.TileParams)),
			sessionID: strings.TrimSpace(protocol.Deref(msg.TileSessionID)),
			anchorID:  protocol.Deref(msg.AnchorID),
			edge:      msg.Edge,
			share:     protocol.Deref(msg.TileShare),
		}
		desktop, err := d.store.UpdateDesktopArrangement(msg.DesktopID, int64(msg.ExpectedRevision), func(desktop setups.Desktop) (setups.Desktop, error) {
			return dockTileOnDesktop(desktop, dock)
		})
		return d.desktopChanged(desktop), err
	})
}

func (d *Daemon) validatedTileParams(kind, params string) (string, error) {
	switch kind {
	case string(layouttree.TileKindBrowser):
		url, err := validateBrowserURL(params)
		if err != nil {
			return "", setups.Errorf(setups.CodeInvalid, "%v", err)
		}
		return url, nil
	case string(layouttree.TileKindNotebook):
		return params, nil
	case string(layouttree.TileKindSeed):
		if err := d.requireHome(garden.Surface); err != nil {
			return "", err
		}
		if _, _, err := d.readSeed(params); err != nil {
			return "", setups.Errorf(setups.CodeNotFound, "%v", err)
		}
		return params, nil
	default:
		return "", setups.Errorf(setups.CodeInvalid, "tile parameters cannot be updated for tile kind %q", kind)
	}
}

type desktopTileUpdate struct {
	tileID    string
	params    string
	sessionID string
}

func (d *Daemon) checkedDesktopTileUpdate(desktopID string, update desktopTileUpdate) (desktopTileUpdate, error) {
	if update.tileID == "" || (update.params == "" && update.sessionID == "") {
		return update, setups.Errorf(setups.CodeInvalid, "updating a tile needs tile_id and tile_params or tile_session_id")
	}
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil {
		return update, err
	}
	tile, found := tileLeafByID(desktop.Tree, update.tileID)
	if !found {
		return update, setups.Errorf(setups.CodeNotFound, "tile %s does not belong to desktop %s", update.tileID, desktopID)
	}
	if update.sessionID != "" && d.store.Get(update.sessionID) == nil {
		return update, setups.Errorf(setups.CodeNotFound, "session %s does not exist", update.sessionID)
	}
	if tile.TileKind == string(layouttree.TileKindMarkdown) {
		if update.sessionID == "" {
			return update, setups.Errorf(setups.CodeInvalid, "a markdown tile keeps its file; only its session can change")
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

func applyDesktopTileUpdate(desktop setups.Desktop, update desktopTileUpdate) (setups.Desktop, error) {
	next, found := desktop.Tree, true
	if update.params != "" {
		next, found = layouttree.UpdateTileParams(next, update.tileID, update.params)
	}
	if found && update.sessionID != "" {
		next, found = layouttree.UpdateTileSessionID(next, update.tileID, update.sessionID)
	}
	if !found {
		return desktop, setups.Errorf(setups.CodeNotFound, "tile %s does not belong to desktop %s", update.tileID, desktop.ID)
	}
	desktop.Tree = next
	return desktop, nil
}

func (d *Daemon) handleDesktopUpdateTile(client *wsClient, msg *protocol.DesktopUpdateTileMessage) {
	d.runSetupAction(client, msg.Cmd, msg.RequestID, func() (setupActionOutcome, error) {
		update, err := d.checkedDesktopTileUpdate(msg.DesktopID, desktopTileUpdate{
			tileID:    strings.TrimSpace(msg.TileID),
			params:    strings.TrimSpace(protocol.Deref(msg.TileParams)),
			sessionID: strings.TrimSpace(protocol.Deref(msg.TileSessionID)),
		})
		if err != nil {
			return setupActionOutcome{}, err
		}
		desktop, err := d.store.UpdateDesktopArrangement(msg.DesktopID, int64(msg.ExpectedRevision), func(desktop setups.Desktop) (setups.Desktop, error) {
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
	tile, found := tileLeafByID(desktop.Tree, tileID)
	if !found {
		return "", setups.Errorf(setups.CodeNotFound, "tile %s does not belong to desktop %s", tileID, desktopID)
	}
	if tile.TileKind != string(layouttree.TileKindMarkdown) {
		return "", setups.Errorf(setups.CodeInvalid, "tile %s is a %s tile; only markdown tiles have content", tileID, tile.TileKind)
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
	if err := d.requireHome("setups and desktops"); err != nil {
		d.sendCommandError(client, msg.Cmd, err.Error())
		return
	}
	path, err := d.desktopMarkdownTilePath(msg.DesktopID, msg.TileID)
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

func (d *Daemon) subscribedDesktopTiles() map[string]map[string]struct{} {
	subscribed := make(map[string]map[string]struct{})
	if d.wsHub == nil {
		return subscribed
	}
	d.wsHub.ForEachClient(func(client *wsClient) {
		for _, key := range client.tileContentSubscriptionKeys() {
			container, tileID, _ := strings.Cut(key, "\x00")
			desktopID, ok := desktopIDFromTileContainer(container)
			if !ok {
				continue
			}
			if subscribed[desktopID] == nil {
				subscribed[desktopID] = make(map[string]struct{})
			}
			subscribed[desktopID][tileID] = struct{}{}
		}
	})
	return subscribed
}

func (d *Daemon) addSubscribedDesktopMarkdownTiles(desired map[string]markdownTileRef) {
	for desktopID, tileIDs := range d.subscribedDesktopTiles() {
		desktop, err := d.store.GetDesktop(desktopID)
		if err != nil {
			continue
		}
		for _, leaf := range layouttree.TileLeaves(desktop.Tree) {
			path := strings.TrimSpace(leaf.TileParams)
			if _, wanted := tileIDs[leaf.TileID]; !wanted || leaf.TileKind != string(layouttree.TileKindMarkdown) || path == "" {
				continue
			}
			desired[tileContentSubscriptionKey(desktopTileContainer(desktopID), leaf.TileID)] = markdownTileRef{desktopID: desktopID, tileID: leaf.TileID, path: path}
		}
	}
}

func (d *Daemon) pruneDesktopTileContentSubscriptions(desktopID string, tree *layouttree.Node) {
	d.pruneTileContentSubscriptionsForLayout(desktopTileContainer(desktopID), tree)
}
