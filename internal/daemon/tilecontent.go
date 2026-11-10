package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const markdownTileIDPrefix = "tile-markdown-"

const seedTileIDPrefix = "tile-seed-"

func markdownTileIDForPath(path string) string {
	sum := sha256.Sum256([]byte(path))
	return markdownTileIDPrefix + hex.EncodeToString(sum[:8])
}

type tileDocument struct {
	host string
	path string
}

type locatedTileDocument struct {
	Host string `json:"host"`
	Path string `json:"path"`
}

func markdownTileDocument(params string) (tileDocument, error) {
	params = strings.TrimSpace(params)
	if params == "" {
		return tileDocument{}, fmt.Errorf("a markdown tile needs the path of its file in tile_params")
	}
	if !strings.HasPrefix(params, "{") {
		return tileDocument{path: params}, nil
	}
	var located locatedTileDocument
	if err := json.Unmarshal([]byte(params), &located); err != nil || located.Host == "" || located.Path == "" {
		return tileDocument{}, fmt.Errorf("markdown tile_params %s are neither a path nor {\"host\", \"path\"}", params)
	}
	return tileDocument{host: located.Host, path: located.Path}, nil
}

func (document tileDocument) params() string {
	if document.host == "" {
		return document.path
	}
	encoded, _ := json.Marshal(locatedTileDocument{Host: document.host, Path: document.path})
	return string(encoded)
}

func (document tileDocument) tileID() string {
	if document.host == "" {
		return markdownTileIDForPath(document.path)
	}
	return markdownTileIDForPath(document.host + "\x00" + document.path)
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

func observeTileContent(path string, sig tileContentSig, now time.Time) (tileContentSig, bool) {
	current := statSig(path)
	if current.mod != sig.mod || current.size != sig.size || current.missing != sig.missing {
		return refreshTileContentHash(path, current, now), true
	}
	if now.Sub(sig.hashCheckedAt) < markdownHashPollInterval {
		return sig, false
	}
	next := refreshTileContentHash(path, current, now)
	return next, next.hasHash != sig.hasHash || next.hash != sig.hash
}

func (d *Daemon) openMarkdownTile(host, path string, callerSessionID protocol.SessionID, focus bool) (desktopID, tileID string, err error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("path must be absolute: %s", path)
	}
	document := tileDocument{host: host, path: filepath.Clean(path)}
	read := protocol.TileDocumentReadMessage{Cmd: protocol.CmdTileDocumentRead, Path: document.path}
	if host != "" {
		read.Host = protocol.Ptr(host)
	}
	if _, err := Call[protocol.TileDocument](d.life.Context(), d.router(), read); err != nil {
		return "", "", d.tileDocumentError(host, err)
	}
	location, err := d.currentAgent(callerSessionID)
	if err != nil {
		return "", "", err
	}
	d.openTileMu.Lock()
	defer d.openTileMu.Unlock()
	desktop, tileID, err := d.openAgentTile(location, agentTile{
		tileID:    document.tileID(),
		tileKind:  string(layouttree.TileKindMarkdown),
		params:    document.params(),
		sessionID: location.sessionID,
	}, focus)
	if err != nil {
		return "", "", err
	}
	if host == "" {
		d.store.RecordFileActivity(document.path, store.FileActivitySourceOpened, location.sessionID)
	}
	return desktop.ID, tileID, nil
}

func (d *Daemon) openSeedTile(seedID string, callerSessionID protocol.SessionID, standalone, focus bool, selectedProfile ...string) (desktopID, tileID string, err error) {
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
		sessionID: d.seedTileSession(seed.Tender, location),
	}, focus)
	if err != nil {
		return "", "", err
	}
	return desktop.ID, tileID, nil
}

func (d *Daemon) seedTileSession(tenderParty who.Party, location agentLocation) protocol.SessionID {
	tenderSessionID, ok := d.liveSessionOf(tenderParty)
	if !ok {
		return location.sessionID
	}
	if tender := d.store.Get(tenderSessionID); tender != nil && tender.ProfileID == location.profileID {
		return tenderSessionID
	}
	return location.sessionID
}

func markdownOpenHost(ctx context.Context, msg *protocol.OpenMarkdownMessage) string {
	if origin, remote := OriginOf(ctx); remote {
		return origin.Host
	}
	return protocol.Deref(msg.Host)
}

func (d *Daemon) handleOpenMarkdown(ctx context.Context, conn net.Conn, msg *protocol.OpenMarkdownMessage) {
	desktopID, tileID, err := d.openMarkdownTile(markdownOpenHost(ctx, msg), msg.Path, protocol.Deref(msg.SessionID), false)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("open_markdown: %v", err))
		return
	}
	d.logf("open_markdown: %s as %s on desktop %s", strings.TrimSpace(msg.Path), tileID, desktopID)
	d.sendOK(conn)
}

func (d *Daemon) handleOpenSeed(conn net.Conn, msg *protocol.OpenSeedMessage) {
	desktopID, tileID, err := d.openSeedTile(msg.SeedID, protocol.Deref(msg.SessionID), protocol.Deref(msg.Standalone), false, protocol.Deref(msg.ProfileID))
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
	return d.on(setting.OpenSentFilesEnabled)
}

func (d *Daemon) handleOpenSentFiles(conn net.Conn, msg *protocol.OpenSentFilesMessage) {
	sessionID := protocol.Deref(msg.SessionID)
	if msg.TerminalID != nil {
		sessionID = d.sessionInTerminal(*msg.TerminalID)
	}
	var paths []string
	for _, path := range msg.Paths {
		path = strings.TrimSpace(path)
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md", ".markdown":
			if filepath.IsAbs(path) {
				paths = append(paths, filepath.Clean(path))
			} else {
				d.logf("open_sent_files: path must be absolute: %s", path)
			}
		default:
			d.logf("open_sent_files: dropped %s (no tile can show it)", path)
		}
	}
	if len(paths) > 0 {
		if monitor := d.sessionMonitor(sessionID); monitor != nil {
			if !monitor.ReportSentFiles(paths) {
				d.sendError(conn, "sent file monitor view was not applied")
				return
			}
		} else {
			for _, path := range paths {
				d.receiveSentFile(sessionID, path)
			}
		}
	}
	d.sendOK(conn)
}

func (d *Daemon) receiveSentFile(sessionID protocol.SessionID, path string) {
	host := d.sessionHost(sessionID)
	if d.openSentFilesEnabled() {
		desktopID, tileID, err := d.openMarkdownTile(host, path, sessionID, false)
		if err == nil {
			d.logf("open_sent_files: %s as %s on desktop %s", path, tileID, desktopID)
			return
		}
		d.logf("open_sent_files: %s: %v", path, err)
	}
	if host != "" {
		return
	}
	if location, err := d.currentAgent(sessionID); err == nil {
		sessionID = location.sessionID
	}
	d.store.RecordFileActivity(path, store.FileActivitySourceOpened, sessionID)
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
	desktopID, tileID, err := d.openMarkdownTile(protocol.Deref(msg.Host), msg.Path, protocol.Deref(msg.SessionID), true)
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
	desktopID, tileID, err := d.openSeedTile(msg.SeedID, protocol.Deref(msg.SessionID), protocol.Deref(msg.Standalone), true, protocol.Deref(msg.ProfileID), client.selectedProfile())
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

func (d *Daemon) runDesktopTileContent(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-d.desktopTiles.nudge:
			d.deliverDesktopTileContent()
		}
	}
}
