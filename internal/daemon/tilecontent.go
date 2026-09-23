package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/workspacelayout"
)

const markdownTileIDPrefix = "tile-markdown-"

const seedTileIDPrefix = "tile-seed-"

func markdownTileIDForPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return markdownTileIDPrefix + hex.EncodeToString(sum[:8])
}

func seedTileIDForID(seedID string) string {
	return seedTileIDPrefix + seedID
}

const markdownPollInterval = 750 * time.Millisecond

const markdownHashPollInterval = 5 * time.Second

const maxMarkdownBytes = 1 << 20

type tileContentSig struct {
	mod           int64
	size          int64
	hash          [sha256.Size]byte
	hasHash       bool
	missing       bool
	hashCheckedAt time.Time
}

type markdownTileRef struct {
	workspaceID string
	tileID      string
	path        string
}

func (d *Daemon) setSelectedSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	d.selectedSessionMu.Lock()
	oldID := d.selectedSessionID
	d.selectedSessionID = sessionID
	if workspaceID, _, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); ok {
		d.selectedWorkspaceID = workspaceID
	} else {
		d.selectedWorkspaceID = ""
	}
	d.selectedSessionMu.Unlock()
	if oldID != sessionID {
		d.updateNudgeSelection(oldID, sessionID)
	}
}

func (d *Daemon) currentlySelectedSession() string {
	d.selectedSessionMu.RLock()
	defer d.selectedSessionMu.RUnlock()
	return d.selectedSessionID
}

func (d *Daemon) setSelectedWorkspace(workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	d.selectedSessionMu.Lock()
	d.selectedWorkspaceID = workspaceID
	d.selectedSessionMu.Unlock()
}

func (d *Daemon) currentlySelectedWorkspace() string {
	d.selectedSessionMu.RLock()
	defer d.selectedSessionMu.RUnlock()
	return d.selectedWorkspaceID
}

func (d *Daemon) tileFilePath(workspaceID, tileID string) (kind, path string, found bool) {
	if d.store == nil {
		return "", "", false
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		return "", "", false
	}
	for _, leaf := range layouttree.TileLeaves(snapshot.Layout) {
		if leaf.TileID == tileID {
			return leaf.TileKind, strings.TrimSpace(leaf.TileParams), true
		}
	}
	return "", "", false
}

func (d *Daemon) tileStillPointsTo(workspaceID, tileID, kind, path string) bool {
	currentKind, currentPath, found := d.tileFilePath(workspaceID, tileID)
	return found && currentKind == kind && currentPath == path
}

func readMarkdownFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("no file is associated with this tile")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", path)
		}
		return "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() > maxMarkdownBytes {
		return "", fmt.Errorf("file is too large to preview (%d bytes, max %d)", info.Size(), maxMarkdownBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMarkdownBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxMarkdownBytes {
		return "", fmt.Errorf("file is too large to preview (more than %d bytes)", maxMarkdownBytes)
	}
	return string(data), nil
}

func statSig(path string) tileContentSig {
	info, err := os.Stat(path)
	if err != nil {
		return tileContentSig{missing: true}
	}
	return tileContentSig{mod: info.ModTime().UnixNano(), size: info.Size()}
}

func refreshTileContentHash(path string, sig tileContentSig, now time.Time) tileContentSig {
	content, err := readMarkdownFile(path)
	if err == nil {
		sig.hash = sha256.Sum256([]byte(content))
		sig.hasHash = true
	}
	sig.hashCheckedAt = now
	return sig
}

func tileContentSubscriptionKey(workspaceID, tileID string) string {
	return workspaceID + "\x00" + tileID
}

const (
	maxTileContentSubscriptions = 128
	tileContentPendingTTL       = 30 * time.Second
)

func (c *wsClient) prunePendingTileContentLocked(now time.Time) {
	for key, createdAt := range c.tileContentPending {
		if now.Sub(createdAt) >= tileContentPendingTTL {
			delete(c.tileContentPending, key)
		}
	}
}

func (c *wsClient) subscribeTileContent(workspaceID, tileID string) bool {
	if c == nil {
		return false
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	if c.tileContentSubscriptions == nil {
		c.tileContentSubscriptions = make(map[string]struct{})
	}
	if _, ok := c.tileContentSubscriptions[key]; ok {
		return true
	}
	if len(c.tileContentSubscriptions) >= maxTileContentSubscriptions {
		return false
	}
	c.tileContentSubscriptions[key] = struct{}{}
	return true
}

func (c *wsClient) notePendingTileContent(workspaceID, tileID string) bool {
	if c == nil {
		return false
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	now := time.Now()
	c.prunePendingTileContentLocked(now)
	if _, ok := c.tileContentSubscriptions[key]; ok {
		return true
	}
	if c.tileContentPending == nil {
		c.tileContentPending = make(map[string]time.Time)
	}
	if _, ok := c.tileContentPending[key]; ok {
		return true
	}
	if len(c.tileContentSubscriptions)+len(c.tileContentPending) >= maxTileContentSubscriptions {
		return false
	}
	c.tileContentPending[key] = now
	return true
}

func (c *wsClient) cancelPendingTileContent(workspaceID, tileID string) {
	if c == nil {
		return
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	delete(c.tileContentPending, key)
}

func (c *wsClient) resolvePendingTileContent(workspaceID, tileID string) bool {
	if c == nil {
		return false
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	c.prunePendingTileContentLocked(time.Now())
	if _, ok := c.tileContentSubscriptions[key]; ok {
		return true
	}
	if _, ok := c.tileContentPending[key]; !ok {
		return false
	}
	delete(c.tileContentPending, key)
	if len(c.tileContentSubscriptions) >= maxTileContentSubscriptions {
		return false
	}
	if c.tileContentSubscriptions == nil {
		c.tileContentSubscriptions = make(map[string]struct{})
	}
	c.tileContentSubscriptions[key] = struct{}{}
	return true
}

func (c *wsClient) wantsTileContent(workspaceID, tileID string) bool {
	if c == nil {
		return false
	}
	key := tileContentSubscriptionKey(workspaceID, tileID)
	c.tileContentMu.RLock()
	defer c.tileContentMu.RUnlock()
	_, ok := c.tileContentSubscriptions[key]
	return ok
}

func (c *wsClient) pruneTileContentSubscriptions(workspaceID string, activeTileIDs map[string]struct{}) {
	if c == nil {
		return
	}
	prefix := strings.TrimSpace(workspaceID) + "\x00"
	c.tileContentMu.Lock()
	defer c.tileContentMu.Unlock()
	for key := range c.tileContentSubscriptions {
		if strings.HasPrefix(key, prefix) {
			if _, ok := activeTileIDs[key]; !ok {
				delete(c.tileContentSubscriptions, key)
			}
		}
	}
	for key := range c.tileContentPending {
		if strings.HasPrefix(key, prefix) {
			if _, ok := activeTileIDs[key]; !ok {
				delete(c.tileContentPending, key)
			}
		}
	}
}

func (d *Daemon) hasTileContentSubscribers(workspaceID, tileID string) bool {
	return d.wsHub != nil && d.wsHub.AnyClientMatches(func(client *wsClient) bool {
		return client.wantsTileContent(workspaceID, tileID)
	})
}

func (d *Daemon) pruneTileContentSubscriptionsForLayout(workspaceID string, layout *layouttree.Node) {
	if d.wsHub == nil {
		return
	}
	activeTileIDs := make(map[string]struct{})
	if layout != nil {
		for _, leaf := range layouttree.TileLeaves(*layout) {
			activeTileIDs[tileContentSubscriptionKey(workspaceID, leaf.TileID)] = struct{}{}
		}
	}
	d.wsHub.ForEachClient(func(client *wsClient) {
		client.pruneTileContentSubscriptions(workspaceID, activeTileIDs)
	})
}

func (d *Daemon) pruneTileContentSubscriptionsForWorkspace(workspaceID string) {
	if d.store == nil {
		return
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		d.pruneTileContentSubscriptionsForLayout(workspaceID, nil)
		return
	}
	d.pruneTileContentSubscriptionsForLayout(workspaceID, &snapshot.Layout)
}

func (d *Daemon) broadcastTileContent(workspaceID, tileID, kind, path, content string, readErr error) {
	if !d.tileStillPointsTo(workspaceID, tileID, kind, path) {
		return
	}
	msg := protocol.WorkspaceTileContentMessage{
		Event:       protocol.EventWorkspaceTileContent,
		WorkspaceID: workspaceID,
		TileID:      tileID,
		TileKind:    kind,
		Path:        path,
		Content:     content,
	}
	if readErr != nil {
		msg.Error = protocol.Ptr(readErr.Error())
	}
	d.wsHub.SendValueToMatchingClients(msg, func(client *wsClient) bool {
		return client.wantsTileContent(workspaceID, tileID)
	})
}

func (d *Daemon) broadcastTileContentNow(workspaceID, tileID string) {
	kind, path, found := d.tileFilePath(workspaceID, tileID)
	if !found || kind != string(layouttree.TileKindMarkdown) {
		return
	}
	content, readErr := readMarkdownFile(path)
	d.broadcastTileContent(workspaceID, tileID, kind, path, content, readErr)
}

func (d *Daemon) handleWorkspaceTileContentGet(client *wsClient, msg *protocol.WorkspaceTileContentGetMessage) {
	kind, _, found := d.tileFilePath(msg.WorkspaceID, msg.TileID)
	if !found {
		d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, fmt.Sprintf("tile not found: %s", msg.TileID))
		return
	}
	if kind != string(layouttree.TileKindMarkdown) {
		d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, fmt.Sprintf("unsupported tile kind: %s", kind))
		return
	}
	if !client.subscribeTileContent(msg.WorkspaceID, msg.TileID) {
		d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, "too many tile content subscriptions")
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		kind, path, found := d.tileFilePath(msg.WorkspaceID, msg.TileID)
		if !found {
			d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, fmt.Sprintf("tile not found: %s", msg.TileID))
			return
		}
		if kind != string(layouttree.TileKindMarkdown) {
			d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, fmt.Sprintf("unsupported tile kind: %s", kind))
			return
		}
		content, readErr := readMarkdownFile(path)
		if !d.tileStillPointsTo(msg.WorkspaceID, msg.TileID, kind, path) {
			continue
		}
		reply := protocol.WorkspaceTileContentMessage{
			Event:       protocol.EventWorkspaceTileContent,
			WorkspaceID: msg.WorkspaceID,
			TileID:      msg.TileID,
			TileKind:    kind,
			Path:        path,
			Content:     content,
		}
		if readErr != nil {
			reply.Error = protocol.Ptr(readErr.Error())
		}
		d.sendToClient(client, reply)
		return
	}
	d.sendCommandError(client, protocol.CmdWorkspaceTileContentGet, "tile changed while content was loading; retry")
}

func (d *Daemon) openMarkdownTile(path, sessionID string) (workspaceID, tileID string, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("path must be absolute: %s", path)
	}
	path = filepath.Clean(path)
	if _, statErr := os.Stat(path); statErr != nil {
		d.store.DeleteFileActivity(path)
		return "", "", fmt.Errorf("file not found: %s", path)
	}
	if sessionID == "" {
		return "", "", fmt.Errorf("no session selected; open a session in attn or pass --session")
	}
	workspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	if !ok {
		return "", "", fmt.Errorf("no workspace found for session %s", sessionID)
	}

	d.openTileMu.Lock()
	defer d.openTileMu.Unlock()

	tileID = markdownTileIDForPath(path)
	alreadyOpen := false
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil {
		alreadyOpen = layouttree.HasTile(snapshot.Layout, tileID)
		if !alreadyOpen {
			for _, leaf := range layouttree.TileLeaves(snapshot.Layout) {
				if leaf.TileKind == string(layouttree.TileKindMarkdown) && leaf.TileParams == path {
					tileID = leaf.TileID
					alreadyOpen = true
					break
				}
			}
		}
	}
	if alreadyOpen {
		if err := d.rebindTileSession(workspaceID, tileID, sessionID); err != nil {
			return "", "", err
		}
	} else if err := d.dockTile(workspaceID, paneID, tileID, string(layouttree.TileKindMarkdown), path, sessionID, protocol.LayoutDockEdgeRight, nil); err != nil {
		return "", "", err
	}
	d.broadcastTileContentNow(workspaceID, tileID)
	d.store.RecordFileActivity(path, store.FileActivitySourceOpened, sessionID)
	return workspaceID, tileID, nil
}

func (d *Daemon) openSeedTile(seedID, placementSessionID string) (workspaceID, tileID string, err error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return "", "", err
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return "", "", err
	}
	bindingSessionID := strings.TrimSpace(seed.TenderSession)
	if bindingSessionID == "" {
		bindingSessionID = placementSessionID
	}

	d.openTileMu.Lock()
	defer d.openTileMu.Unlock()

	tileID = seedTileIDForID(seed.ID)
	if placementSessionID == "" {
		for _, candidateID := range d.store.WorkspaceLayoutIDs() {
			if snapshot := d.store.GetWorkspaceLayout(candidateID); snapshot != nil && isStandaloneSeedReader(snapshot, tileID) {
				layout, ok := layouttree.UpdateTileParams(snapshot.Layout, tileID, seed.ID)
				if !ok {
					return "", "", fmt.Errorf("tile not found: %s", tileID)
				}
				layout, ok = layouttree.UpdateTileSessionID(layout, tileID, bindingSessionID)
				if !ok {
					return "", "", fmt.Errorf("tile not found: %s", tileID)
				}
				snapshot.Layout = layout
				if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
					return "", "", err
				}
				d.broadcastWorkspaceLayoutUpdated(candidateID)
				return candidateID, tileID, nil
			}
		}

		workspaceID = uuid.NewString()
		d.registerWorkspace(workspaceID, seed.Title, "", false)
		snapshot := workspacelayout.NormalizeWorkspaceLayout(workspacelayout.WorkspaceLayout{
			WorkspaceID: workspaceID,
			Layout: layouttree.Node{
				Type:          "tile",
				TileID:        tileID,
				TileKind:      string(layouttree.TileKindSeed),
				TileParams:    seed.ID,
				TileSessionID: bindingSessionID,
			},
		})
		if err := d.store.SaveWorkspaceLayout(snapshot); err != nil {
			d.unregisterWorkspaceIfEmpty(workspaceID)
			return "", "", err
		}
		d.broadcastWorkspaceLayoutUpdated(workspaceID)
		return workspaceID, tileID, nil
	}

	workspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(placementSessionID)
	if !ok {
		return "", "", fmt.Errorf("no workspace found for session %s", placementSessionID)
	}
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil && layouttree.HasTile(snapshot.Layout, tileID) {
		if err := d.rebindTileSession(workspaceID, tileID, bindingSessionID); err != nil {
			return "", "", err
		}
	} else if err := d.dockTile(workspaceID, paneID, tileID, string(layouttree.TileKindSeed), seed.ID, bindingSessionID, protocol.LayoutDockEdgeRight, nil); err != nil {
		return "", "", err
	}
	return workspaceID, tileID, nil
}

func (d *Daemon) rebindTileSession(workspaceID, tileID, sessionID string) error {
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		return fmt.Errorf("workspace not found: %s", workspaceID)
	}
	if current, ok := layouttree.TileSessionIDByID(snapshot.Layout, tileID); ok && current == sessionID {
		return nil
	}
	layout, ok := layouttree.UpdateTileSessionID(snapshot.Layout, tileID, sessionID)
	if !ok {
		return fmt.Errorf("tile not found: %s", tileID)
	}
	snapshot.Layout = layout
	if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
		return err
	}
	d.broadcastWorkspaceLayoutUpdated(workspaceID)
	return nil
}

func (d *Daemon) handleOpenMarkdown(conn net.Conn, msg *protocol.OpenMarkdownMessage) {
	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	if sessionID == "" {
		sessionID = d.currentlySelectedSession()
	}
	workspaceID, tileID, err := d.openMarkdownTile(msg.Path, sessionID)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("open_markdown: %v", err))
		return
	}
	d.logf("open_markdown: docked %s as %s into workspace %s (session %s)", strings.TrimSpace(msg.Path), tileID, workspaceID, sessionID)
	d.sendOK(conn)
}

func (d *Daemon) seedPlacementSession(msg *protocol.OpenSeedMessage) string {
	placementSessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	if placementSessionID == "" && !protocol.Deref(msg.Standalone) {
		placementSessionID = d.currentlySelectedSession()
	}
	return placementSessionID
}

func (d *Daemon) handleOpenSeed(conn net.Conn, msg *protocol.OpenSeedMessage) {
	workspaceID, tileID, err := d.openSeedTile(msg.SeedID, d.seedPlacementSession(msg))
	if err != nil {
		d.sendError(conn, fmt.Sprintf("open_seed: %v", err))
		return
	}
	d.logf("open_seed: docked %s as %s into workspace %s", strings.TrimSpace(msg.SeedID), tileID, workspaceID)
	d.sendOK(conn)
}

func (d *Daemon) openSentFilesEnabled() bool {
	if d.store == nil {
		return true
	}
	raw := strings.TrimSpace(d.store.GetSetting(SettingOpenSentFilesEnabled))
	if raw == "" {
		return true
	}
	return parseBooleanSetting(raw)
}

func (d *Daemon) handleOpenSentFiles(conn net.Conn, msg *protocol.OpenSentFilesMessage) {
	if !d.openSentFilesEnabled() {
		d.sendOK(conn)
		return
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	if sessionID == "" {
		sessionID = d.currentlySelectedSession()
	}
	for _, path := range msg.Paths {
		path = strings.TrimSpace(path)
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".markdown":
			workspaceID, tileID, err := d.openMarkdownTile(path, sessionID)
			if err != nil {
				d.logf("open_sent_files: %s: %v", path, err)
				continue
			}
			d.logf("open_sent_files: docked %s as %s into workspace %s (session %s)", path, tileID, workspaceID, sessionID)
		default:
			d.logf("open_sent_files: dropped %s (no tile can show it)", path)
		}
	}
	d.sendOK(conn)
}

func (d *Daemon) handleOpenMarkdownWS(client *wsClient, msg *protocol.OpenMarkdownMessage) {
	result := protocol.OpenMarkdownResultMessage{
		Event:   protocol.EventOpenMarkdownResult,
		Success: true,
		Path:    strings.TrimSpace(msg.Path),
	}
	if requestID := strings.TrimSpace(protocol.Deref(msg.RequestID)); requestID != "" {
		result.RequestID = protocol.Ptr(requestID)
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	if sessionID == "" {
		sessionID = d.currentlySelectedSession()
	}
	workspaceID, tileID, err := d.openMarkdownTile(msg.Path, sessionID)
	if err != nil {
		result.Success = false
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.WorkspaceID = protocol.Ptr(workspaceID)
	result.TileID = protocol.Ptr(tileID)
	d.logf("open_markdown(ws): docked %s as %s into workspace %s (session %s)", result.Path, tileID, workspaceID, sessionID)
	d.sendToClient(client, result)
}

func (d *Daemon) handleOpenSeedWS(client *wsClient, msg *protocol.OpenSeedMessage) {
	result := protocol.OpenSeedResultMessage{
		Event:     protocol.EventOpenSeedResult,
		RequestID: msg.RequestID,
		SeedID:    strings.TrimSpace(msg.SeedID),
		Success:   true,
	}
	workspaceID, tileID, err := d.openSeedTile(msg.SeedID, d.seedPlacementSession(msg))
	if err != nil {
		result.Success = false
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.WorkspaceID = protocol.Ptr(workspaceID)
	result.TileID = protocol.Ptr(tileID)
	d.logf("open_seed(ws): docked %s as %s into workspace %s", result.SeedID, tileID, workspaceID)
	d.sendToClient(client, result)
}

func (d *Daemon) runMarkdownContentWatcher(done <-chan struct{}) {
	ticker := time.NewTicker(markdownPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-d.desktopTiles.nudge:
			d.deliverDesktopTileContent()
		case <-ticker.C:
			d.pollMarkdownOnce()
			d.deliverDesktopTileContent()
		}
	}
}

func (d *Daemon) pollMarkdownOnce() {
	for _, ref := range d.collectChangedMarkdownTiles() {
		content, readErr := readMarkdownFile(ref.path)
		d.broadcastTileContent(ref.workspaceID, ref.tileID, string(layouttree.TileKindMarkdown), ref.path, content, readErr)
	}
}

func (d *Daemon) collectChangedMarkdownTiles() []markdownTileRef {
	if d.store == nil {
		return nil
	}

	desired := make(map[string]markdownTileRef)
	for _, workspaceID := range d.store.WorkspaceLayoutIDs() {
		snapshot := d.store.GetWorkspaceLayout(workspaceID)
		if snapshot == nil {
			continue
		}
		for _, leaf := range layouttree.TileLeaves(snapshot.Layout) {
			if leaf.TileKind != string(layouttree.TileKindMarkdown) {
				continue
			}
			if !d.hasTileContentSubscribers(workspaceID, leaf.TileID) {
				continue
			}
			path := strings.TrimSpace(leaf.TileParams)
			if path == "" {
				continue
			}
			key := workspaceID + "\x00" + leaf.TileID
			desired[key] = markdownTileRef{workspaceID: workspaceID, tileID: leaf.TileID, path: path}
		}
	}

	d.markdownSeenMu.Lock()
	defer d.markdownSeenMu.Unlock()
	if d.markdownSeen == nil {
		d.markdownSeen = make(map[string]tileContentSig)
	}
	for key := range d.markdownSeen {
		if _, ok := desired[key]; !ok {
			delete(d.markdownSeen, key)
		}
	}
	var changed []markdownTileRef
	now := time.Now()
	for key, ref := range desired {
		sig := statSig(ref.path)
		prev, had := d.markdownSeen[key]
		if !had || prev.mod != sig.mod || prev.size != sig.size || prev.missing != sig.missing {
			d.markdownSeen[key] = refreshTileContentHash(ref.path, sig, now)
			changed = append(changed, ref)
			continue
		}
		if now.Sub(prev.hashCheckedAt) < markdownHashPollInterval {
			continue
		}
		next := refreshTileContentHash(ref.path, sig, now)
		d.markdownSeen[key] = next
		if prev.hasHash != next.hasHash || prev.hash != next.hash {
			changed = append(changed, ref)
		}
	}
	return changed
}

func isStandaloneSeedReader(snapshot *workspacelayout.WorkspaceLayout, tileID string) bool {
	return len(snapshot.Panes) == 0 && snapshot.Layout.Type == "tile" && snapshot.Layout.TileID == tileID
}
