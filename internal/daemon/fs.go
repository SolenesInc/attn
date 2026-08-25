package daemon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// Bounds the whole marshaled fs_read_asset_result message — the unit that hits the
// WebSocket, which has no other outbound cap. The raw read cap below derives from it.
const maxAssetMessageBytes = 8 << 20

const assetEnvelopeSlack = 4 << 10

// base64 of n bytes is 4*ceil(n/3).
const maxAssetBytes = (maxAssetMessageBytes - assetEnvelopeSlack) / 4 * 3

// The contract, not a convenience lookup: unlike mime.TypeByExtension it excludes
// everything non-image, so this surface cannot widen Tauri's fs permissions.
var assetMimeTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
}

// A non-empty root is gated on the authenticated attn app before it is even validated —
// otherwise any local WebSocket client could read or overwrite any file in the user's home.
func (d *Daemon) resolveFsRoot(client *wsClient, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return d.notebookRoot()
	}
	if !client.isTrustedAppClient() {
		return "", fmt.Errorf("fs root requires the authenticated attn app client")
	}
	resolved, err := normalizeExternalRoot(raw)
	if err != nil {
		return "", fmt.Errorf("fs root %w", err)
	}
	return resolved, nil
}

// Cached per resolved root so writes to one root serialize through a single in-process writer.
func (d *Daemon) fsStoreFor(client *wsClient, rawRoot string) (*fsdoc.Store, string, error) {
	root, err := d.resolveFsRoot(client, rawRoot)
	if err != nil {
		return nil, "", err
	}
	d.fsMu.Lock()
	if d.fsStores == nil {
		d.fsStores = make(map[string]*fsdoc.Store)
	}
	store, ok := d.fsStores[root]
	if !ok {
		store = fsdoc.NewStore(root)
		d.fsStores[root] = store
	}
	d.fsMu.Unlock()
	if notebookRoot, nerr := d.notebookRoot(); nerr == nil && root == notebookRoot {
		d.ensureNotebookWatcher(root)
	}
	return store, root, nil
}

// A non-notebook root's absolute path and changed paths are sensitive, so the event goes
// only to clients holding an fs_watch ref on it.
func (d *Daemon) broadcastFsChanged(root, origin string, paths ...string) {
	msg := protocol.FsChangedMessage{
		Event:  protocol.EventFsChanged,
		Paths:  paths,
		Origin: origin,
		Root:   root,
	}
	if d.isNotebookRoot(root) {
		d.broadcastMessage(msg)
		return
	}
	d.sendFsChangedToWatchers(root, msg)
}

func (d *Daemon) sendFsListWSResult(client *wsClient, requestID, path, rawRoot string) {
	var entries []protocol.FsEntry
	store, _, err := d.fsStoreFor(client, rawRoot)
	if err == nil {
		var list []fsdoc.Entry
		if list, err = store.List(path); err == nil {
			entries = fsEntriesToProtocol(list)
		}
	}
	msg := protocol.FsListResultMessage{
		Event:     protocol.EventFsListResult,
		RequestID: requestID,
		Success:   err == nil,
		Entries:   entries,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendFsReadWSResult(client *wsClient, requestID, path, rawRoot string) {
	var result *protocol.FsReadResult
	store, _, err := d.fsStoreFor(client, rawRoot)
	if err == nil {
		var content []byte
		var hash string
		if content, hash, err = store.Read(path); err == nil {
			result = &protocol.FsReadResult{Path: path, Content: string(content), Hash: hash}
		}
	}
	msg := protocol.FsReadResultMessage{
		Event:     protocol.EventFsReadResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendFsReadAssetWSResult(client *wsClient, requestID, path, rawRoot string) {
	var result *protocol.FsReadAssetResult
	mimeType, err := assetMimeTypeFor(path)
	if err == nil {
		var store *fsdoc.Store
		if store, _, err = d.fsStoreFor(client, rawRoot); err == nil {
			var content []byte
			if content, _, err = store.ReadWithLimit(path, maxAssetBytes); err == nil {
				if len(content) > maxAssetBytes {
					err = fmt.Errorf("asset exceeds the %d byte read cap", maxAssetBytes)
				} else if fits, ferr := assetMessageFits(requestID, path, mimeType, len(content)); ferr != nil {
					err = ferr
				} else if !fits {
					err = fmt.Errorf("asset response exceeds the %d byte message cap", maxAssetMessageBytes)
				} else {
					result = &protocol.FsReadAssetResult{
						Path:       path,
						MimeType:   mimeType,
						DataBase64: base64.StdEncoding.EncodeToString(content),
					}
				}
			}
		}
	}
	msg := protocol.FsReadAssetResultMessage{
		Event:     protocol.EventFsReadAssetResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func assetMimeTypeFor(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	mimeType, ok := assetMimeTypes[ext]
	if !ok {
		return "", errors.New("not a supported image asset")
	}
	return mimeType, nil
}

// Exact, not an estimate: base64 output never needs JSON escaping, so the length is the
// empty-payload envelope plus EncodedLen(rawLen).
func assetMessageFits(requestID, path, mimeType string, rawLen int) (bool, error) {
	probe := protocol.FsReadAssetResultMessage{
		Event:     protocol.EventFsReadAssetResult,
		RequestID: requestID,
		Success:   true,
		Result:    &protocol.FsReadAssetResult{Path: path, MimeType: mimeType},
	}
	envelope, err := json.Marshal(probe)
	if err != nil {
		return false, err
	}
	return len(envelope)+base64.StdEncoding.EncodedLen(rawLen) <= maxAssetMessageBytes, nil
}

// A conflict is a successful result carrying conflict=true, not an error.
func (d *Daemon) sendFsWriteWSResult(client *wsClient, requestID, path, content, baseHash, rawRoot string) {
	var result *protocol.FsWriteResult
	store, root, err := d.fsStoreFor(client, rawRoot)
	if err == nil {
		changed := path
		if rel, cerr := fsdoc.CleanPath(path); cerr == nil {
			changed = rel
		}
		var hash string
		var conflict *fsdoc.Conflict
		if hash, conflict, err = store.Write(path, []byte(content), baseHash); err == nil {
			result = &protocol.FsWriteResult{Path: changed}
			if conflict != nil {
				result.Conflict = true
				if conflict.CurrentHash != "" {
					result.CurrentHash = protocol.Ptr(conflict.CurrentHash)
				}
			} else {
				result.Hash = protocol.Ptr(hash)
				if d.isNotebookRoot(root) {
					// Content-aware self-write so the shared watcher does not surface this UI edit as an external one.
					d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: changed, Hash: hash})
				} else if w := d.fsWatcherFor(root); w != nil {
					w.NoteSelfWrite(notebook.SelfWrite{Rel: changed, Hash: hash})
				}
				d.broadcastFsChanged(root, originUI, changed)
			}
		}
	}
	msg := protocol.FsWriteResultMessage{
		Event:     protocol.EventFsWriteResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendFsRenameWSResult(client *wsClient, requestID, oldPath, newPath, rawRoot string) {
	oldRel, oldErr := fsdoc.CleanPath(oldPath)
	newRel, newErr := fsdoc.CleanPath(newPath)
	err := errors.Join(oldErr, newErr)
	var result *protocol.FsRenameResult
	store, root, storeErr := d.fsStoreFor(client, rawRoot)
	if err == nil {
		err = storeErr
	}
	if err == nil {
		_, hash, readErr := store.Read(oldRel)
		if readErr != nil {
			err = readErr
		} else {
			inNotebook := d.isNotebookRoot(root)
			if inNotebook {
				d.noteNotebookSelfWrite(
					notebook.SelfWrite{Rel: oldRel},
					notebook.SelfWrite{Rel: newRel, Hash: hash},
				)
			} else if w := d.fsWatcherFor(root); w != nil {
				w.NoteSelfWrite(
					notebook.SelfWrite{Rel: oldRel},
					notebook.SelfWrite{Rel: newRel, Hash: hash},
				)
			}
			err = store.Rename(oldRel, newRel)
			if err == nil {
				result = &protocol.FsRenameResult{Path: oldRel, NewPath: newRel}
				if inNotebook {
					d.broadcastNotebookChanged(originUI, oldRel, newRel)
				}
				d.broadcastFsChanged(root, originUI, oldRel, newRel)
			}
		}
	}
	msg := protocol.FsRenameResultMessage{Event: protocol.EventFsRenameResult, RequestID: requestID, Success: err == nil, Result: result}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendFsDeleteWSResult(client *wsClient, requestID, path, rawRoot string) {
	rel, err := fsdoc.CleanPath(path)
	var result *protocol.FsDeleteResult
	store, root, storeErr := d.fsStoreFor(client, rawRoot)
	if err == nil {
		err = storeErr
	}
	if err == nil {
		inNotebook := d.isNotebookRoot(root)
		if inNotebook {
			d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: rel})
		} else if w := d.fsWatcherFor(root); w != nil {
			w.NoteSelfWrite(notebook.SelfWrite{Rel: rel})
		}
		err = store.Delete(rel)
		if err == nil {
			result = &protocol.FsDeleteResult{Path: rel}
			if inNotebook {
				d.broadcastNotebookChanged(originUI, rel)
			}
			d.broadcastFsChanged(root, originUI, rel)
		}
	}
	msg := protocol.FsDeleteResultMessage{Event: protocol.EventFsDeleteResult, RequestID: requestID, Success: err == nil, Result: result}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// A path that fails to validate is an error the frontend treats as "unknown, leave the
// link unflagged", not as "missing".
func (d *Daemon) sendFsExistsWSResult(client *wsClient, requestID, path, rawRoot string) {
	var result *protocol.FsExistsResult
	store, _, err := d.fsStoreFor(client, rawRoot)
	if err == nil {
		var exists bool
		if exists, err = store.Exists(path); err == nil {
			result = &protocol.FsExistsResult{Path: path, Exists: exists}
		}
	}
	msg := protocol.FsExistsResultMessage{
		Event:     protocol.EventFsExistsResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) isNotebookRoot(root string) bool {
	notebookRoot, err := d.notebookRoot()
	return err == nil && root == notebookRoot
}

func fsEntriesToProtocol(entries []fsdoc.Entry) []protocol.FsEntry {
	out := make([]protocol.FsEntry, len(entries))
	for i, e := range entries {
		out[i] = protocol.FsEntry{
			Path:  e.Path,
			Name:  e.Name,
			IsDir: e.IsDir,
			Size:  int(e.Size),
		}
		if e.Modified != "" {
			out[i].Modified = protocol.Ptr(e.Modified)
		}
	}
	return out
}
