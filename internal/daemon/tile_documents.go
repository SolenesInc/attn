package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type watchedTileDocument struct {
	sig     tileContentSig
	version int
}

type tileDocumentWatch struct {
	mu       sync.Mutex
	versions int
	watched  map[string]*watchedTileDocument
	wake     chan struct{}
}

func newTileDocumentWatch() tileDocumentWatch {
	return tileDocumentWatch{watched: map[string]*watchedTileDocument{}, wake: make(chan struct{}, 1)}
}

func (d *Daemon) watchTileDocuments(_ context.Context, msg protocol.TileDocumentsWatchMessage) (protocol.TileDocumentVersions, error) {
	w := &d.tileDocuments
	now := time.Now()
	answer := protocol.TileDocumentVersions{Documents: make([]protocol.TileDocumentVersion, 0, len(msg.Documents))}
	w.mu.Lock()
	listed := make(map[string]*watchedTileDocument, len(msg.Documents))
	for _, path := range msg.Documents {
		doc := w.watched[path]
		if doc == nil {
			w.versions++
			doc = &watchedTileDocument{sig: refreshTileContentHash(path, statSig(path), now), version: w.versions}
		}
		listed[path] = doc
		answer.Documents = append(answer.Documents, protocol.TileDocumentVersion{Path: path, Version: doc.version})
	}
	w.watched = listed
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return answer, nil
}

func (w *tileDocumentWatch) observe(now time.Time) []protocol.TileDocumentVersion {
	w.mu.Lock()
	defer w.mu.Unlock()
	var changed []protocol.TileDocumentVersion
	for path, doc := range w.watched {
		next, differs := observeTileContent(path, doc.sig, now)
		doc.sig = next
		if differs {
			w.versions++
			doc.version = w.versions
			changed = append(changed, protocol.TileDocumentVersion{Path: path, Version: doc.version})
		}
	}
	return changed
}

func (w *tileDocumentWatch) idle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.watched) == 0
}

func (w *tileDocumentWatch) version(path string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if doc := w.watched[path]; doc != nil {
		return doc.version
	}
	return 0
}

func (d *Daemon) runTileDocumentWatcher(done <-chan struct{}) {
	w := &d.tileDocuments
	ticker := time.NewTicker(markdownPollInterval)
	defer ticker.Stop()
	for {
		if w.idle() {
			ticker.Stop()
			select {
			case <-done:
				return
			case <-w.wake:
			}
			ticker.Reset(markdownPollInterval)
			continue
		}
		select {
		case <-done:
			return
		case <-w.wake:
			continue
		case <-ticker.C:
		}
		for _, changed := range w.observe(time.Now()) {
			report := protocol.TileDocumentChangedMessage{Cmd: protocol.CmdTileDocumentChanged, Path: changed.Path, Version: changed.Version}
			if _, err := Call[struct{}](d.life.Context(), d.router(), report); err != nil {
				d.logf("tile document %s changed: %v", changed.Path, err)
			}
		}
	}
}

func (d *Daemon) readTileDocument(_ context.Context, msg protocol.TileDocumentReadMessage) (protocol.TileDocument, error) {
	if _, err := os.Stat(msg.Path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return protocol.TileDocument{}, fmt.Errorf("file not found: %s", msg.Path)
		}
		return protocol.TileDocument{}, err
	}
	answer := protocol.TileDocument{Version: d.tileDocuments.version(msg.Path)}
	content, err := readMarkdownFile(msg.Path)
	answer.Content = content
	if err != nil {
		answer.Error = protocol.Ptr(err.Error())
	}
	return answer, nil
}

func (d *Daemon) readTileAsset(_ context.Context, msg protocol.TileAssetReadMessage) (protocol.TileAssetReadResultMessage, error) {
	if !filepath.IsAbs(msg.Path) {
		return protocol.TileAssetReadResultMessage{}, fmt.Errorf("image path must be absolute: %s", msg.Path)
	}
	mimeType, err := assetMimeTypeFor(msg.Path)
	if err != nil {
		return protocol.TileAssetReadResultMessage{}, fmt.Errorf("%s: %w", msg.Path, err)
	}
	file, err := os.OpenFile(msg.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return protocol.TileAssetReadResultMessage{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return protocol.TileAssetReadResultMessage{}, err
	}
	if !info.Mode().IsRegular() {
		return protocol.TileAssetReadResultMessage{}, fmt.Errorf("not a regular file: %s", msg.Path)
	}
	if info.Size() > maxAssetBytes {
		return protocol.TileAssetReadResultMessage{}, fmt.Errorf("image %s is %d bytes; a markdown tile loads images up to %d bytes", msg.Path, info.Size(), maxAssetBytes)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxAssetBytes+1))
	if err != nil {
		return protocol.TileAssetReadResultMessage{}, err
	}
	if len(content) > maxAssetBytes {
		return protocol.TileAssetReadResultMessage{}, fmt.Errorf("image %s is over %d bytes; a markdown tile loads images up to %d bytes", msg.Path, maxAssetBytes, maxAssetBytes)
	}
	return protocol.TileAssetReadResultMessage{
		Event:   protocol.EventTileAssetReadResult,
		Success: true,
		DataUri: protocol.Ptr("data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(content)),
	}, nil
}

func (d *Daemon) handleTileAssetReadWS(client *wsClient, msg *protocol.TileAssetReadMessage) {
	d.life.Go("handleTileAssetReadWS", func() {
		result, err := Call[protocol.TileAssetReadResultMessage](d.life.Context(), d.router(), *msg)
		if err != nil {
			result = protocol.TileAssetReadResultMessage{Event: protocol.EventTileAssetReadResult, Error: protocol.Ptr(d.tileDocumentError(protocol.Deref(msg.Host), err).Error())}
		}
		result.RequestID = msg.RequestID
		d.sendToClient(client, &result)
	})
}
