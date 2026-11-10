package daemon

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

type desktopTileKey struct {
	desktopID string
	tileID    string
}

type desktopMarkdownTile struct {
	key      desktopTileKey
	document tileDocument
}

type heldTileDocument struct {
	version int
	newest  int
	reading bool
	serial  int
	content string
	err     error
}

type deliveredTileFile struct {
	document tileDocument
	serial   int
}

type tileWatcher struct {
	changed chan struct{}
	conn    *bridge.Conn
}

type desktopTileDelivery struct {
	mu        sync.Mutex
	nudge     chan struct{}
	documents map[tileDocument]*heldTileDocument
	watching  map[string]*tileWatcher
	delivered map[*wsClient]map[desktopTileKey]deliveredTileFile
}

func newDesktopTileDelivery() desktopTileDelivery {
	return desktopTileDelivery{
		nudge:     make(chan struct{}, 1),
		documents: map[tileDocument]*heldTileDocument{},
		watching:  map[string]*tileWatcher{},
		delivered: map[*wsClient]map[desktopTileKey]deliveredTileFile{},
	}
}

func (d *Daemon) nudgeDesktopTileContent() {
	select {
	case d.desktopTiles.nudge <- struct{}{}:
	default:
	}
}

func markdownTilesOnCurrentDesktop(profile profiles.Profile, desktops []profiles.Desktop) []desktopMarkdownTile {
	var tiles []desktopMarkdownTile
	for _, desktop := range desktops {
		if desktop.ID != profile.CurrentDesktopID {
			continue
		}
		for _, leaf := range layouttree.TileLeaves(desktop.Tree) {
			if leaf.TileKind != string(layouttree.TileKindMarkdown) {
				continue
			}
			if document, err := markdownTileDocument(leaf.TileParams); err == nil {
				tiles = append(tiles, desktopMarkdownTile{key: desktopTileKey{desktopID: desktop.ID, tileID: leaf.TileID}, document: document})
			}
		}
	}
	return tiles
}

// arrangementDelivery is what an arrangement carries besides its bytes.
type arrangementDelivery struct {
	shown []desktopMarkdownTile
	// The store read it came from: a client is never sent one older than it already has.
	seq int64
	// Its content without request id: a client already holding it is not sent it again.
	print [sha256.Size]byte
}

type arrangementDelivered struct {
	evicted      bool
	shownChanged bool
}

func (h *wsHub) deliverArrangement(client *wsClient, message outboundMessage, delivery *arrangementDelivery) arrangementDelivered {
	client.arrangementMu.Lock()
	defer client.arrangementMu.Unlock()
	if client.arrangementsHeld > 0 {
		client.arrangementsMissed = true
		return arrangementDelivered{}
	}
	if delivery.seq <= client.arrangementSeq {
		return arrangementDelivered{}
	}
	if delivery.print == client.arrangementPrint {
		client.arrangementSeq = delivery.seq
		return arrangementDelivered{}
	}
	return h.deliverArrangementLocked(client, message, delivery)
}

func (h *wsHub) deliverArrangementLocked(client *wsClient, message outboundMessage, delivery *arrangementDelivery) arrangementDelivered {
	queued, evicted := h.deliver(client, message)
	outcome := arrangementDelivered{evicted: evicted}
	if queued {
		outcome.shownChanged = !slices.Equal(client.shownTiles, delivery.shown)
		client.shownTiles = delivery.shown
		client.arrangementPrint = delivery.print
		client.arrangementSeq = delivery.seq
	}
	return outcome
}

func desktopTileContentMessage(key desktopTileKey, document tileDocument, held *heldTileDocument) protocol.DesktopTileContentMessage {
	message := protocol.DesktopTileContentMessage{
		Event:     protocol.EventDesktopTileContent,
		DesktopID: key.desktopID,
		TileID:    key.tileID,
		TileKind:  string(layouttree.TileKindMarkdown),
		Path:      document.path,
		Content:   held.content,
	}
	if document.host != "" {
		message.Host = protocol.Ptr(document.host)
	}
	if held.err != nil {
		message.Error = protocol.Ptr(held.err.Error())
	}
	return message
}

func (d *Daemon) deliverDesktopTileContent() {
	var clients []*wsClient
	if d.wsHub != nil {
		d.wsHub.ForEachClient(func(client *wsClient) { clients = append(clients, client) })
	}
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	connected := map[*wsClient]bool{}
	shown := map[tileDocument]bool{}
	for _, client := range clients {
		connected[client] = true
		d.deliverToClient(client, shown)
	}
	for client := range delivery.delivered {
		if !connected[client] {
			delete(delivery.delivered, client)
		}
	}
	for document := range delivery.documents {
		if !shown[document] {
			delete(delivery.documents, document)
			delivery.signalHostLocked(document.host)
		}
	}
}

func (d *Daemon) deliverToClient(client *wsClient, shown map[tileDocument]bool) {
	delivery := &d.desktopTiles
	client.arrangementMu.Lock()
	defer client.arrangementMu.Unlock()
	delivered := delivery.delivered[client]
	onShown := map[desktopTileKey]bool{}
	for _, tile := range client.shownTiles {
		onShown[tile.key] = true
		shown[tile.document] = true
		held := delivery.documents[tile.document]
		if held == nil {
			held = d.holdTileDocumentLocked(tile.document)
		}
		if held.serial == 0 {
			continue
		}
		want := deliveredTileFile{document: tile.document, serial: held.serial}
		if delivered[tile.key] == want {
			continue
		}
		if !d.sendToClient(client, desktopTileContentMessage(tile.key, tile.document, held)) {
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

func (d *Daemon) holdTileDocumentLocked(document tileDocument) *heldTileDocument {
	delivery := &d.desktopTiles
	held := &heldTileDocument{}
	delivery.documents[document] = held
	delivery.signalHostLocked(document.host)
	if _, watched := delivery.watching[document.host]; !watched && document.host != "" {
		if _, _, err := d.outpostConn(document.host); err != nil {
			held.err = err
			held.serial++
		}
	}
	return held
}

func (delivery *desktopTileDelivery) signalHostLocked(host string) {
	watcher := delivery.watching[host]
	if watcher == nil {
		return
	}
	select {
	case watcher.changed <- struct{}{}:
	default:
	}
}

func (d *Daemon) keepTileDocumentsWatched(ctx context.Context, host string, conn *bridge.Conn) {
	delivery := &d.desktopTiles
	watcher := &tileWatcher{changed: make(chan struct{}, 1), conn: conn}
	delivery.mu.Lock()
	delivery.watching[host] = watcher
	for document, held := range delivery.documents {
		if document.host == host {
			delivery.documents[document] = &heldTileDocument{serial: held.serial, content: held.content, err: held.err}
		}
	}
	delivery.mu.Unlock()
	defer func() {
		delivery.mu.Lock()
		if delivery.watching[host] == watcher {
			delete(delivery.watching, host)
			if host != "" && d.life.Context().Err() == nil {
				d.failTileDocumentsLocked(host, fmt.Errorf("%s is unreachable", d.outpostName(host)))
			}
		}
		delivery.mu.Unlock()
		d.nudgeDesktopTileContent()
	}()
	for {
		delivery.mu.Lock()
		message := protocol.TileDocumentsWatchMessage{Cmd: protocol.CmdTileDocumentsWatch, Documents: []string{}}
		for document := range delivery.documents {
			if document.host == host {
				message.Documents = append(message.Documents, document.path)
			}
		}
		delivery.mu.Unlock()
		if host != "" {
			message.Host = protocol.Ptr(host)
		}
		answer, err := callTileDocuments[protocol.TileDocumentVersions](ctx, d, conn, message)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			d.logf("tile documents on %q: %v", host, err)
			d.failTileDocuments(host, d.tileDocumentError(host, err))
		}
		for _, document := range answer.Documents {
			d.tileDocumentReported(tileDocument{host: host, path: document.Path}, document.Version)
		}
		select {
		case <-watcher.changed:
		case <-ctx.Done():
			return
		}
	}
}

func callTileDocuments[R any](ctx context.Context, d *Daemon, conn *bridge.Conn, msg protocol.Placed) (R, error) {
	if conn == nil {
		return Call[R](ctx, d.router(), msg)
	}
	var answer R
	err := conn.Call(ctx, bridgeCall, msg, &answer)
	return answer, terminalBridgeError(err)
}

func (d *Daemon) failTileDocuments(host string, err error) {
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	d.failTileDocumentsLocked(host, err)
	delivery.mu.Unlock()
	d.nudgeDesktopTileContent()
}

func (d *Daemon) failTileDocumentsLocked(host string, err error) {
	for document, held := range d.desktopTiles.documents {
		if document.host == host {
			held.content, held.err = "", err
			held.serial++
		}
	}
}

func (d *Daemon) tileDocumentChanged(ctx context.Context, msg protocol.TileDocumentChangedMessage) (struct{}, error) {
	origin, _ := OriginOf(ctx)
	d.tileDocumentReported(tileDocument{host: origin.Host, path: msg.Path}, msg.Version)
	return struct{}{}, nil
}

func (d *Daemon) tileDocumentReported(document tileDocument, version int) {
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	held := delivery.documents[document]
	if held == nil {
		return
	}
	held.newest = max(held.newest, version)
	d.readStaleTileDocumentLocked(document, held)
}

func (d *Daemon) readStaleTileDocumentLocked(document tileDocument, held *heldTileDocument) {
	if held.reading || held.newest <= held.version {
		return
	}
	held.reading = true
	target := held.newest
	var conn *bridge.Conn
	if watcher := d.desktopTiles.watching[document.host]; watcher != nil {
		conn = watcher.conn
	}
	d.life.Go("readTileDocument", func() {
		message := protocol.TileDocumentReadMessage{Cmd: protocol.CmdTileDocumentRead, Path: document.path}
		if document.host != "" {
			message.Host = protocol.Ptr(document.host)
		}
		answer, err := callTileDocuments[protocol.TileDocument](d.life.Context(), d, conn, message)
		if err != nil {
			err = d.tileDocumentError(document.host, err)
		} else if answer.Error != nil {
			err = errors.New(*answer.Error)
		}
		delivery := &d.desktopTiles
		delivery.mu.Lock()
		held.reading = false
		if delivery.documents[document] == held {
			held.version = max(target, answer.Version)
			held.content, held.err = answer.Content, err
			held.serial++
			d.readStaleTileDocumentLocked(document, held)
		}
		delivery.mu.Unlock()
		d.nudgeDesktopTileContent()
	})
}

func (d *Daemon) tileDocumentError(host string, err error) error {
	if host == "" {
		return err
	}
	name := d.outpostName(host)
	if rejected := (*bridge.CallRejected)(nil); errors.As(err, &rejected) {
		return fmt.Errorf("%s: %s", name, rejected.Reason)
	}
	return err
}
