package daemon

import (
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
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
	shown     map[string][]desktopMarkdownTile
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

func (d *Daemon) clientsByProfile() map[string][]*wsClient {
	byProfile := map[string][]*wsClient{}
	if d.wsHub == nil {
		return byProfile
	}
	d.wsHub.ForEachClient(func(client *wsClient) {
		if profileID := client.selectedProfile(); profileID != "" {
			byProfile[profileID] = append(byProfile[profileID], client)
		}
	})
	return byProfile
}

func (d *Daemon) markdownTilesOnCurrentDesktop(profileID string) []desktopMarkdownTile {
	if d.requireHome("profiles and desktops") != nil {
		return nil
	}
	profile, desktops, err := d.store.ProfileArrangement(profileID)
	if err != nil {
		d.logf("desktop tile content: reading profile %s: %v", profileID, err)
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

func (d *Daemon) deliverDesktopTileContent(rereadArrangements bool) {
	clients := d.clientsByProfile()
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if rereadArrangements || delivery.shown == nil {
		delivery.shown = map[string][]desktopMarkdownTile{}
	}
	for profileID := range delivery.shown {
		if _, connected := clients[profileID]; !connected {
			delete(delivery.shown, profileID)
		}
	}
	for profileID := range clients {
		if _, read := delivery.shown[profileID]; !read {
			delivery.shown[profileID] = d.markdownTilesOnCurrentDesktop(profileID)
		}
	}
	delivery.forgetWhatIsNoLongerShown(clients)
	now := time.Now()
	for profileID, tiles := range delivery.shown {
		for _, tile := range tiles {
			file := delivery.currentFile(tile, now)
			var content string
			var readErr error
			read := false
			for _, client := range clients[profileID] {
				want := deliveredTileFile{path: file.path, version: file.version}
				if delivery.delivered[client][tile.key] == want {
					continue
				}
				if !read {
					content, readErr = readMarkdownFile(file.path)
					read = true
				}
				d.sendToClient(client, desktopTileContentMessage(tile.key.desktopID, tile.key.tileID, file.path, content, readErr))
				if delivery.delivered[client] == nil {
					delivery.delivered[client] = map[desktopTileKey]deliveredTileFile{}
				}
				delivery.delivered[client][tile.key] = want
			}
		}
	}
}

func (delivery *desktopTileDelivery) forgetWhatIsNoLongerShown(clients map[string][]*wsClient) {
	shownKeys := map[desktopTileKey]bool{}
	for _, tiles := range delivery.shown {
		for _, tile := range tiles {
			shownKeys[tile.key] = true
		}
	}
	for key := range delivery.files {
		if !shownKeys[key] {
			delete(delivery.files, key)
		}
	}
	connected := map[*wsClient]string{}
	for profileID, profileClients := range clients {
		for _, client := range profileClients {
			connected[client] = profileID
		}
	}
	if delivery.delivered == nil {
		delivery.delivered = map[*wsClient]map[desktopTileKey]deliveredTileFile{}
	}
	for client, files := range delivery.delivered {
		profileID, stillConnected := connected[client]
		if !stillConnected {
			delete(delivery.delivered, client)
			continue
		}
		onProfile := map[desktopTileKey]bool{}
		for _, tile := range delivery.shown[profileID] {
			onProfile[tile.key] = true
		}
		for key := range files {
			if !onProfile[key] {
				delete(files, key)
			}
		}
	}
}

func (delivery *desktopTileDelivery) currentFile(tile desktopMarkdownTile, now time.Time) *desktopTileFile {
	if delivery.files == nil {
		delivery.files = map[desktopTileKey]*desktopTileFile{}
	}
	file := delivery.files[tile.key]
	if file == nil || file.path != tile.path {
		file = &desktopTileFile{path: tile.path, sig: refreshTileContentHash(tile.path, statSig(tile.path), now), version: 1}
		if previous := delivery.files[tile.key]; previous != nil {
			file.version = previous.version + 1
		}
		delivery.files[tile.key] = file
		return file
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
