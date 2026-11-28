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
	"github.com/victorarias/attn/internal/review"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/viewlang"
)

// documentKinds says which kinds of document a tile reads as more than
// markdown, by the experimental settings that are on.
type documentKinds struct {
	review bool
	view   bool
}

func (d *Daemon) documentKinds() documentKinds {
	return documentKinds{review: d.on(setting.CodeReviewEnabled), view: d.on(setting.ViewsEnabled)}
}

type watchedTileDocument struct {
	sig     tileContentSig
	kinds   documentKinds
	shown   map[string]tileContentSig
	version int
}

// watchShown follows the files a document shows besides itself, a review
// document's stops or a view's data files, so their changes reach the tile as
// a new version of the document.
func (doc *watchedTileDocument) watchShown(path string, kinds documentKinds, now time.Time) {
	doc.kinds, doc.shown = kinds, nil
	if kinds == (documentKinds{}) {
		return
	}
	content, err := readMarkdownFile(path)
	if err != nil {
		return
	}
	var files []string
	switch {
	case kinds.view && viewlang.IsView(content):
		files = viewFiles(path, content)
	case kinds.review && review.IsReview(content):
		files = review.StopFiles(content)
	}
	doc.shown = map[string]tileContentSig{}
	for _, file := range files {
		doc.shown[file] = refreshTileContentHash(file, statSig(file), now)
	}
}

func (doc *watchedTileDocument) observe(path string, kinds documentKinds, now time.Time) bool {
	next, differs := observeTileContent(path, doc.sig, now)
	doc.sig = next
	for file, sig := range doc.shown {
		next, shownDiffers := observeTileContent(file, sig, now)
		doc.shown[file] = next
		differs = differs || shownDiffers
	}
	if differs || kinds != doc.kinds {
		doc.watchShown(path, kinds, now)
		return true
	}
	return false
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
	kinds := d.documentKinds()
	d.viewRuns.setStarted(msg.Started)
	w.mu.Lock()
	listed := make(map[string]*watchedTileDocument, len(msg.Documents))
	for _, path := range msg.Documents {
		doc := w.watched[path]
		if doc == nil {
			w.versions++
			doc = &watchedTileDocument{sig: refreshTileContentHash(path, statSig(path), now), version: w.versions}
			doc.watchShown(path, kinds, now)
		}
		listed[path] = doc
	}
	var unlisted []string
	for path := range w.watched {
		if listed[path] == nil {
			unlisted = append(unlisted, path)
		}
	}
	w.watched = listed
	w.mu.Unlock()
	for _, path := range unlisted {
		d.releaseCodeNavigation(path)
		d.followViewRuns(path)
	}
	var changed []string
	for _, path := range msg.Documents {
		if d.followViewRuns(path) {
			changed = append(changed, path)
		}
	}
	w.bump(changed)
	answer := protocol.TileDocumentVersions{Documents: make([]protocol.TileDocumentVersion, 0, len(msg.Documents))}
	for _, path := range msg.Documents {
		answer.Documents = append(answer.Documents, protocol.TileDocumentVersion{Path: path, Version: w.version(path)})
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return answer, nil
}

func (w *tileDocumentWatch) observe(kinds documentKinds, now time.Time) []protocol.TileDocumentVersion {
	w.mu.Lock()
	defer w.mu.Unlock()
	var changed []protocol.TileDocumentVersion
	for path, doc := range w.watched {
		if doc.observe(path, kinds, now) {
			w.versions++
			doc.version = w.versions
			changed = append(changed, protocol.TileDocumentVersion{Path: path, Version: doc.version})
		}
	}
	return changed
}

// bump gives each watched document of paths a new version, so its tile reads
// it again.
func (w *tileDocumentWatch) bump(paths []string) []protocol.TileDocumentVersion {
	w.mu.Lock()
	defer w.mu.Unlock()
	var changed []protocol.TileDocumentVersion
	for _, path := range paths {
		if doc := w.watched[path]; doc != nil {
			w.versions++
			doc.version = w.versions
			changed = append(changed, protocol.TileDocumentVersion{Path: path, Version: doc.version})
		}
	}
	return changed
}

// bumpAll marks every watched document changed.
func (w *tileDocumentWatch) bumpAll() []protocol.TileDocumentVersion {
	w.mu.Lock()
	paths := make([]string, 0, len(w.watched))
	for path := range w.watched {
		paths = append(paths, path)
	}
	w.mu.Unlock()
	return w.bump(paths)
}

func (d *Daemon) reportTileDocuments(changed []protocol.TileDocumentVersion) {
	for _, document := range changed {
		report := protocol.TileDocumentChangedMessage{Cmd: protocol.CmdTileDocumentChanged, Path: document.Path, Version: document.Version}
		if _, err := Call[struct{}](d.life.Context(), d.router(), report); err != nil {
			d.logf("tile document %s changed: %v", document.Path, err)
		}
	}
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
		changed := w.observe(d.documentKinds(), time.Now())
		for _, document := range changed {
			d.followViewRuns(document.Path)
		}
		d.reportTileDocuments(changed)
	}
}

func (d *Daemon) readTileDocument(ctx context.Context, msg protocol.TileDocumentReadMessage) (protocol.TileDocument, error) {
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
		return answer, nil
	}
	kinds := d.documentKinds()
	if kinds.view && viewlang.IsView(content) {
		d.releaseCodeNavigation(msg.Path)
		answer.View = d.readView(msg.Path, content)
		return answer, nil
	}
	if !kinds.review || !review.IsReview(content) {
		d.releaseCodeNavigation(msg.Path)
		return answer, nil
	}
	bundle, err := d.resolveReview(ctx, msg.Path, content)
	if err != nil {
		return answer, err
	}
	if answer.Version > 0 {
		d.needCodeNavigation(msg.Path, &bundle)
	}
	answer.Review = &bundle
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

func (d *Daemon) handleTileDocumentReadWS(client *wsClient, msg *protocol.TileDocumentReadMessage) {
	d.life.Go("handleTileDocumentReadWS", func() {
		result := protocol.TileDocumentReadResultMessage{Event: protocol.EventTileDocumentReadResult, RequestID: msg.RequestID}
		document, err := Call[protocol.TileDocument](d.life.Context(), d.router(), *msg)
		switch {
		case err != nil:
			result.Error = protocol.Ptr(d.tileDocumentError(protocol.Deref(msg.Host), err).Error())
		case document.Error != nil:
			result.Error = document.Error
		default:
			result.Success = true
			result.Content = protocol.Ptr(document.Content)
		}
		d.sendToClient(client, &result)
	})
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
