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

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
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

func (d *Daemon) openMarkdownTile(path string, callerSessionID protocol.SessionID) (desktopID, tileID string, err error) {
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
	location, err := d.currentAgent(callerSessionID)
	if err != nil {
		return "", "", err
	}
	d.openTileMu.Lock()
	defer d.openTileMu.Unlock()
	desktop, tileID, err := d.openAgentTile(location, agentTile{
		tileID:    markdownTileIDForPath(path),
		tileKind:  string(layouttree.TileKindMarkdown),
		params:    path,
		sessionID: location.sessionID,
	})
	if err != nil {
		return "", "", err
	}
	d.store.RecordFileActivity(path, store.FileActivitySourceOpened, location.sessionID)
	return desktop.ID, tileID, nil
}

func (d *Daemon) openSeedTile(seedID string, callerSessionID protocol.SessionID, standalone bool, selectedProfile ...string) (desktopID, tileID string, err error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return "", "", err
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return "", "", err
	}
	selected := ""
	if len(selectedProfile) > 1 {
		selected = selectedProfile[1]
	}
	profile, err := d.resolveGardenProfile(callerSessionID, firstProfile(selectedProfile), selected)
	if err != nil {
		return "", "", err
	}
	if selected != "" && profile.ID != selected {
		owner, err := d.store.GetProfile(selected)
		if err != nil {
			return "", "", err
		}
		return "", "", fmt.Errorf("source session belongs to profile %q; app belongs to profile %q", profile.Name, owner.Name)
	}
	if err := d.requireSeedInProfile(seedID, profile.ID, false); err != nil {
		return "", "", err
	}
	location := agentLocation{profileID: profile.ID, desktopID: profile.CurrentDesktopID}
	if callerSessionID != "" && !standalone {
		location, err = d.agentLocation(callerSessionID)
		if err != nil {
			return "", "", err
		}
	}
	d.openTileMu.Lock()
	defer d.openTileMu.Unlock()
	desktop, tileID, err := d.openAgentTile(location, agentTile{
		tileID:    seedTileIDForID(seed.ID),
		tileKind:  string(layouttree.TileKindSeed),
		params:    seed.ID,
		sessionID: d.seedTileSession(seed.TenderSession, location),
	})
	if err != nil {
		return "", "", err
	}
	return desktop.ID, tileID, nil
}

func (d *Daemon) seedTileSession(tenderSessionID protocol.SessionID, location agentLocation) protocol.SessionID {
	tenderSessionID = protocol.TrimID(tenderSessionID)
	if tenderSessionID == "" {
		return location.sessionID
	}
	if tender := d.store.Get(tenderSessionID); tender != nil && tender.ProfileID == location.profileID {
		return tenderSessionID
	}
	return location.sessionID
}

func (d *Daemon) handleOpenMarkdown(conn net.Conn, msg *protocol.OpenMarkdownMessage) {
	desktopID, tileID, err := d.openMarkdownTile(msg.Path, protocol.Deref(msg.SessionID))
	if err != nil {
		d.sendError(conn, fmt.Sprintf("open_markdown: %v", err))
		return
	}
	d.logf("open_markdown: %s as %s on desktop %s", strings.TrimSpace(msg.Path), tileID, desktopID)
	d.sendOK(conn)
}

func (d *Daemon) handleOpenSeed(conn net.Conn, msg *protocol.OpenSeedMessage) {
	desktopID, tileID, err := d.openSeedTile(msg.SeedID, protocol.Deref(msg.SessionID), protocol.Deref(msg.Standalone), protocol.Deref(msg.ProfileID))
	if err != nil {
		d.sendError(conn, fmt.Sprintf("open_seed: %v", err))
		return
	}
	d.logf("open_seed: %s as %s on desktop %s", strings.TrimSpace(msg.SeedID), tileID, desktopID)
	d.sendOK(conn)
}

func (d *Daemon) openSentFilesEnabled() bool {
	if d.store == nil {
		return true
	}
	raw := strings.TrimSpace(d.daemonSetting(settingOpenSentFilesEnabled))
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
	sessionID := protocol.Deref(msg.SessionID)
	if msg.TerminalID != nil {
		sessionID = d.sessionInTerminal(*msg.TerminalID)
	}
	for _, path := range msg.Paths {
		path = strings.TrimSpace(path)
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".markdown":
			desktopID, tileID, err := d.openMarkdownTile(path, sessionID)
			if err != nil {
				d.logf("open_sent_files: %s: %v", path, err)
				continue
			}
			d.logf("open_sent_files: %s as %s on desktop %s", path, tileID, desktopID)
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
	client.holdArrangements()
	defer d.releaseArrangements(client)
	desktopID, tileID, err := d.openMarkdownTile(msg.Path, protocol.Deref(msg.SessionID))
	if err != nil {
		result.Success = false
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.DesktopID = protocol.Ptr(desktopID)
	result.TileID = protocol.Ptr(tileID)
	d.logf("open_markdown(ws): %s as %s on desktop %s", result.Path, tileID, desktopID)
	d.sendArrangement(client, protocol.Deref(result.RequestID), nil)
	d.sendToClient(client, result)
}

func (d *Daemon) handleOpenSeedWS(client *wsClient, msg *protocol.OpenSeedMessage) {
	result := protocol.OpenSeedResultMessage{
		Event:     protocol.EventOpenSeedResult,
		RequestID: msg.RequestID,
		SeedID:    strings.TrimSpace(msg.SeedID),
		Success:   true,
	}
	client.holdArrangements()
	defer d.releaseArrangements(client)
	desktopID, tileID, err := d.openSeedTile(msg.SeedID, protocol.Deref(msg.SessionID), protocol.Deref(msg.Standalone), protocol.Deref(msg.ProfileID), client.selectedProfile())
	if err != nil {
		result.Success = false
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	result.DesktopID = protocol.Ptr(desktopID)
	result.TileID = protocol.Ptr(tileID)
	d.logf("open_seed(ws): %s as %s on desktop %s", result.SeedID, tileID, desktopID)
	d.sendArrangement(client, protocol.Deref(msg.RequestID), nil)
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
			d.deliverDesktopTileContent()
		}
	}
}
