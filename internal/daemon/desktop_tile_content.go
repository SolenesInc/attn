package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

type desktopTileKey struct {
	desktopID string
	tileID    string
}

type desktopMarkdownTile struct {
	key  desktopTileKey
	path string
}

type desktopTileFile struct {
	path    string
	sig     tileContentSig
	version int
}

type deliveredTileFile struct {
	path    string
	version int
}

type desktopTileDelivery struct {
	mu        sync.Mutex
	nudge     chan struct{}
	files     map[desktopTileKey]*desktopTileFile
	delivered map[*wsClient]map[desktopTileKey]deliveredTileFile
}

func newDesktopTileDelivery() desktopTileDelivery {
	return desktopTileDelivery{nudge: make(chan struct{}, 1)}
}

func (d *Daemon) nudgeDesktopTileContent() {
	select {
	case d.desktopTiles.nudge <- struct{}{}:
	default:
	}
}

func (d *Daemon) shownMarkdownTiles(profile profiles.Profile, desktops []profiles.Desktop) []desktopMarkdownTile {
	if d.requireHome("profiles and desktops") != nil {
		return nil
	}
	var tiles []desktopMarkdownTile
	for _, desktop := range desktops {
		if desktop.ID != profile.CurrentDesktopID {
			continue
		}
		for _, leaf := range layouttree.TileLeaves(desktop.Tree) {
			path := strings.TrimSpace(leaf.TileParams)
			if leaf.TileKind == string(layouttree.TileKindMarkdown) && path != "" {
				tiles = append(tiles, desktopMarkdownTile{key: desktopTileKey{desktopID: desktop.ID, tileID: leaf.TileID}, path: path})
			}
		}
	}
	return tiles
}

func (c *wsClient) trySendArrangement(message outboundMessage, shown func(*wsClient) []desktopMarkdownTile) bool {
	if shown == nil {
		return c.trySend(message)
	}
	c.arrangementMu.Lock()
	defer c.arrangementMu.Unlock()
	if !c.trySend(message) {
		return false
	}
	c.shownTiles = shown(c)
	return true
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

type tileRead struct {
	content string
	err     error
}

func (d *Daemon) deliverDesktopTileContent() {
	var clients []*wsClient
	if d.wsHub != nil {
		d.wsHub.ForEachClient(func(client *wsClient) { clients = append(clients, client) })
	}
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if delivery.delivered == nil {
		delivery.delivered = map[*wsClient]map[desktopTileKey]deliveredTileFile{}
	}
	if delivery.files == nil {
		delivery.files = map[desktopTileKey]*desktopTileFile{}
	}
	connected := map[*wsClient]bool{}
	shownKeys := map[desktopTileKey]bool{}
	checked := map[desktopTileKey]*desktopTileFile{}
	reads := map[desktopTileKey]tileRead{}
	now := time.Now()
	for _, client := range clients {
		connected[client] = true
		d.deliverToClient(client, now, shownKeys, checked, reads)
	}
	for client := range delivery.delivered {
		if !connected[client] {
			delete(delivery.delivered, client)
		}
	}
	for key := range delivery.files {
		if !shownKeys[key] {
			delete(delivery.files, key)
		}
	}
}

func (d *Daemon) deliverToClient(client *wsClient, now time.Time, shownKeys map[desktopTileKey]bool, checked map[desktopTileKey]*desktopTileFile, reads map[desktopTileKey]tileRead) {
	delivery := &d.desktopTiles
	client.arrangementMu.Lock()
	defer client.arrangementMu.Unlock()
	delivered := delivery.delivered[client]
	onShown := map[desktopTileKey]bool{}
	for _, tile := range client.shownTiles {
		onShown[tile.key] = true
		shownKeys[tile.key] = true
		file := checked[tile.key]
		if file == nil {
			file = delivery.currentFile(tile, now)
			checked[tile.key] = file
		}
		want := deliveredTileFile{path: file.path, version: file.version}
		if delivered[tile.key] == want {
			continue
		}
		read, done := reads[tile.key]
		if !done {
			read.content, read.err = readMarkdownFile(file.path)
			reads[tile.key] = read
		}
		if !d.sendToClient(client, desktopTileContentMessage(tile.key.desktopID, tile.key.tileID, file.path, read.content, read.err)) {
			continue
		}
		if delivered == nil {
			delivered = map[desktopTileKey]deliveredTileFile{}
			delivery.delivered[client] = delivered
		}
		delivered[tile.key] = want
	}
	for key := range delivered {
		if !onShown[key] {
			delete(delivered, key)
		}
	}
}

func (delivery *desktopTileDelivery) currentFile(tile desktopMarkdownTile, now time.Time) *desktopTileFile {
	file := delivery.files[tile.key]
	if file == nil || file.path != tile.path {
		next := &desktopTileFile{path: tile.path, sig: refreshTileContentHash(tile.path, statSig(tile.path), now), version: 1}
		if file != nil {
			next.version = file.version + 1
		}
		delivery.files[tile.key] = next
		return next
	}
	sig := statSig(file.path)
	if sig.mod != file.sig.mod || sig.size != file.sig.size || sig.missing != file.sig.missing {
		file.sig = refreshTileContentHash(file.path, sig, now)
		file.version++
		return file
	}
	if now.Sub(file.sig.hashCheckedAt) < markdownHashPollInterval {
		return file
	}
	next := refreshTileContentHash(file.path, sig, now)
	if next.hasHash != file.sig.hasHash || next.hash != file.sig.hash {
		file.version++
	}
	file.sig = next
	return file
}
