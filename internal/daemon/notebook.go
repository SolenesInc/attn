package daemon

import (
	"encoding/json"
	"fmt"
	"github.com/victorarias/attn/internal/bus"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fsdoc"
	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

const originAgent = "agent"

const originExternal = "external"

const originUI = "ui"

// Cached so writes serialize through one in-process writer.
func (d *Daemon) notebookStoreFor() (*notebook.Store, error) {
	root, err := d.notebookRoot()
	if err != nil {
		return nil, err
	}
	d.notebookMu.Lock()
	if d.notebookStore == nil || d.notebookStore.Root() != root {
		d.notebookStore = notebook.NewStore(root)
	}
	store := d.notebookStore
	d.notebookMu.Unlock()
	// Must run after releasing notebookMu; ensureNotebookWatcher takes its own.
	d.ensureNotebookWatcher(root)
	return store, nil
}

func (d *Daemon) ensureNotebookWatcher(root string) {
	// Never resurrect during shutdown: a watcher started after Stop() closes d.done leaks a goroutine and an fd.
	// started after that leaks a goroutine and an fd.
	select {
	case <-d.done:
		return
	default:
	}
	d.notebookWatcherMu.Lock()
	defer d.notebookWatcherMu.Unlock()
	if d.notebookWatcher != nil && d.notebookWatchedRoot == root {
		return
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return
	}
	if d.notebookWatcher != nil {
		_ = d.notebookWatcher.Close()
		d.notebookWatcher = nil
		d.notebookWatchedRoot = ""
	}
	w, err := notebook.NewWatcherWithCleaner(root, notebook.DefaultWatchDebounce, fsdoc.CleanPath, func(paths []string) {
		d.broadcastFsChanged(root, originExternal, paths...)
		var mdPaths []string
		for _, p := range paths {
			if _, err := notebook.CleanPath(p); err == nil {
				mdPaths = append(mdPaths, p)
			}
		}
		if len(mdPaths) > 0 {
			d.broadcastNotebookChanged(originExternal, mdPaths...)
		}
	})
	if err != nil {
		d.logf("notebook watcher: failed to watch %s: %v", root, err)
		return
	}
	d.notebookWatcher = w
	d.notebookWatchedRoot = root
}

func (d *Daemon) noteNotebookSelfWrite(writes ...notebook.SelfWrite) {
	d.notebookWatcherMu.Lock()
	w := d.notebookWatcher
	d.notebookWatcherMu.Unlock()
	w.NoteSelfWrite(writes...)
}

func (d *Daemon) stopNotebookWatcher() {
	d.notebookWatcherMu.Lock()
	w := d.notebookWatcher
	d.notebookWatcher = nil
	d.notebookWatchedRoot = ""
	d.notebookWatcherMu.Unlock()
	_ = w.Close()
}

func (d *Daemon) notebookRoot() (string, error) {
	if configured := strings.TrimSpace(d.store.GetSetting(SettingNotebookRoot)); configured != "" {
		if strings.HasPrefix(configured, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve home directory: %w", err)
			}
			return filepath.Join(home, configured[2:]), nil
		}
		// Canonical form is what the store's containment checks expect.
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return notebook.DefaultRoot(home, config.Profile()), nil
}

func (d *Daemon) broadcastNotebookChanged(origin string, paths ...string) {
	d.coalesceSnapshots(func() {
		for _, path := range paths {
			d.publishFact(FactNotebookFileChanged, path, notebookChangeOrigin{Origin: origin})
		}
	})
}

type notebookChangeOrigin struct {
	Origin string `json:"origin"`
}

func (d *Daemon) projectNotebookChanged(ev bus.Event) {
	change, ok := decodeFact[notebookChangeOrigin](d, ev)
	if !ok {
		return
	}
	d.notebookPendingMu.Lock()
	if d.notebookPendingPaths == nil {
		d.notebookPendingPaths = map[string][]string{}
	}
	d.notebookPendingPaths[change.Origin] = append(d.notebookPendingPaths[change.Origin], ev.Subject)
	d.notebookPendingMu.Unlock()

	d.projectSnapshot("notebook_changed:"+change.Origin, func() {
		d.notebookPendingMu.Lock()
		paths := d.notebookPendingPaths[change.Origin]
		delete(d.notebookPendingPaths, change.Origin)
		d.notebookPendingMu.Unlock()
		if len(paths) == 0 {
			return
		}
		d.broadcastMessage(protocol.NotebookChangedMessage{
			Event:  protocol.EventNotebookChanged,
			Paths:  paths,
			Origin: change.Origin,
		})
	})
}

func (d *Daemon) ensureNotebookScaffold() (root string, created bool, err error) {
	store, err := d.notebookStoreFor()
	if err != nil {
		return "", false, err
	}
	createdPaths, scaffoldErr := store.EnsureScaffold()
	if len(createdPaths) > 0 {
		// Exactly the files written, never all reserved paths: recording those would suppress real external edits.
		writes := make([]notebook.SelfWrite, len(createdPaths))
		for i, p := range createdPaths {
			writes[i] = notebook.SelfWrite{Rel: p}
		}
		d.noteNotebookSelfWrite(writes...)
		d.broadcastNotebookChanged(originAgent, createdPaths...)
		d.ensureNotebookWatcher(store.Root())
	}
	if scaffoldErr != nil {
		return "", false, scaffoldErr
	}
	return store.Root(), len(createdPaths) > 0, nil
}

func (d *Daemon) handleNotebookGuide(conn net.Conn, msg *protocol.NotebookGuideMessage) {
	root, err := d.notebookRoot()
	if err != nil {
		d.sendError(conn, "notebook: "+err.Error())
		return
	}
	sessionID := strings.TrimSpace(protocol.Deref(msg.SessionID))
	sessionIsChief := sessionID != "" && sessionID == d.chiefOfStaffSessionID()
	if sessionIsChief {
		if _, _, serr := d.ensureNotebookScaffold(); serr != nil {
			d.logf("notebook guide: ensure scaffold failed: %v", serr)
		}
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		NotebookGuide: &protocol.NotebookGuideResult{
			Guidance:       hooks.ChiefGuidance(root),
			Root:           root,
			SessionIsChief: sessionIsChief,
		},
	})
}

func (d *Daemon) sendNotebookListWSResult(client *wsClient, requestID, prefix string) {
	var entries []protocol.NotebookEntry
	store, err := d.notebookStoreFor()
	if err == nil {
		var list []notebook.Entry
		if list, err = store.List(prefix); err == nil {
			entries = notebookEntriesToProtocol(list)
		}
	}
	msg := protocol.NotebookListResultMessage{
		Event:     protocol.EventNotebookListResult,
		RequestID: requestID,
		Success:   err == nil,
		Entries:   entries,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendNotebookReadWSResult(client *wsClient, requestID, path string) {
	var result *protocol.NotebookReadResult
	store, err := d.notebookStoreFor()
	if err == nil {
		var content []byte
		var hash string
		if content, hash, err = store.Read(path); err == nil {
			result = &protocol.NotebookReadResult{Path: path, Content: string(content), Hash: hash}
		}
	}
	msg := protocol.NotebookReadResultMessage{
		Event:     protocol.EventNotebookReadResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func (d *Daemon) sendNotebookBacklinksWSResult(client *wsClient, requestID, path string) {
	var entries []protocol.NotebookEntry
	store, err := d.notebookStoreFor()
	if err == nil {
		var list []notebook.Entry
		if list, err = store.Backlinks(path); err == nil {
			entries = notebookEntriesToProtocol(list)
		}
	}
	msg := protocol.NotebookBacklinksResultMessage{
		Event:     protocol.EventNotebookBacklinksResult,
		RequestID: requestID,
		Success:   err == nil,
		Entries:   entries,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

// A conflict is a successful result carrying conflict=true, not an error.
func (d *Daemon) sendNotebookWriteWSResult(client *wsClient, requestID, path, content, baseHash string) {
	var result *protocol.NotebookWriteResult
	store, err := d.notebookStoreFor()
	if err == nil {
		changed := path
		if rel, cerr := notebook.CleanPath(path); cerr == nil {
			changed = rel
		}
		var hash string
		var conflict *notebook.Conflict
		if hash, conflict, err = store.Write(path, []byte(content), baseHash); err == nil {
			result = &protocol.NotebookWriteResult{Path: changed}
			if conflict != nil {
				result.Conflict = true
				if conflict.CurrentHash != "" {
					result.CurrentHash = protocol.Ptr(conflict.CurrentHash)
				}
			} else {
				result.Hash = protocol.Ptr(hash)
				d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: changed, Hash: hash})
				d.broadcastNotebookChanged(originUI, changed)
			}
		}
	}
	msg := protocol.NotebookWriteResultMessage{
		Event:     protocol.EventNotebookWriteResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

const maxInboxSelection = 32 << 10

func chiefInboxNudgePrompt(root string) string {
	return fmt.Sprintf("A new selection was added to your Notebook inbox. Read %s to see it.", filepath.Join(root, notebook.FileInbox))
}

func (d *Daemon) sendNotebookToChiefWSResult(client *wsClient, requestID, sourcePath, selection string) {
	var result *protocol.NotebookSendToChiefResult
	store, err := d.notebookStoreFor()
	if err == nil {
		if strings.TrimSpace(selection) == "" {
			err = fmt.Errorf("notebook: empty selection")
		} else if len(selection) > maxInboxSelection {
			err = fmt.Errorf("notebook: selection exceeds %d bytes", maxInboxSelection)
		}
	}
	if err == nil {
		var relPath, hash string
		if relPath, hash, err = store.AppendInbox(formatChiefInboxEntry(sourcePath, selection)); err == nil {
			d.noteNotebookSelfWrite(notebook.SelfWrite{Rel: relPath, Hash: hash})
			d.broadcastNotebookChanged(originUI, relPath)
			result = &protocol.NotebookSendToChiefResult{
				Path:   relPath,
				Nudged: d.nudgeChiefOfStaff(requestID, chiefInboxNudgePrompt(store.Root())),
			}
		}
	}
	msg := protocol.NotebookSendToChiefResultMessage{
		Event:     protocol.EventNotebookSendToChiefResult,
		RequestID: requestID,
		Success:   err == nil,
		Result:    result,
	}
	if err != nil {
		msg.Error = protocol.Ptr(err.Error())
	}
	d.sendToClient(client, msg)
}

func formatChiefInboxEntry(sourcePath, selection string) string {
	var b strings.Builder
	b.WriteString(chiefInboxSourceHeading(sourcePath))
	b.WriteString("\n\n")
	// A non-UI client's CRLF would leave a stray CR on every blockquoted line.
	normalized := strings.ReplaceAll(selection, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	for _, line := range strings.Split(strings.TrimRight(normalized, "\n"), "\n") {
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// CleanPath permits markdown-corrupting characters, so anything risky renders as code.
func chiefInboxSourceHeading(sourcePath string) string {
	rel, err := notebook.CleanPath(sourcePath)
	if err != nil {
		return "## From the Notebook"
	}
	rel = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '`' {
			return -1
		}
		return r
	}, rel)
	if rel == "" {
		return "## From the Notebook"
	}
	// The link parser stops a target at the first ')' or whitespace.
	if strings.IndexAny(rel, " \t()[]<>") < 0 {
		return fmt.Sprintf("## From [/%s](/%s)", rel, rel)
	}
	return fmt.Sprintf("## From `/%s`", rel)
}

func notebookEntriesToProtocol(entries []notebook.Entry) []protocol.NotebookEntry {
	out := make([]protocol.NotebookEntry, 0, len(entries))
	for _, e := range entries {
		pe := protocol.NotebookEntry{Path: e.Path, Size: int(e.Size)}
		if e.Type != "" {
			pe.Type = protocol.Ptr(e.Type)
		}
		if e.Title != "" {
			pe.Title = protocol.Ptr(e.Title)
		}
		if e.Summary != "" {
			pe.Summary = protocol.Ptr(e.Summary)
		}
		if e.Updated != "" {
			pe.Updated = protocol.Ptr(e.Updated)
		}
		out = append(out, pe)
	}
	return out
}
